package ingressohttp_test

import (
	"net/http"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/entrada/ingressohttp"
	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/instalacao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/contrapressao"
)

// ajustesSaturaveis produzem uma portaria com UMA vaga e NENHUMA fila.
//
// Com eles, basta segurar a vaga para que a proxima remessa seja recusada — sem
// gerar carga, sem depender de temporizacao e sem que o resultado mude conforme a
// velocidade do disco da maquina de quem roda o teste.
func ajustesSaturaveis() contrapressao.Ajustes {
	return contrapressao.Ajustes{
		VagasSimultaneas:   1,
		FilaMaxima:         0,
		OrcamentoComum:     2 * time.Second,
		OrcamentoReservado: 10 * time.Second,
		EsperaMinima:       3 * time.Second,
		EsperaMaxima:       30 * time.Second,
	}
}

// envelopeDeEventoDiscreto monta uma mudanca de estado de maquina.
//
// A classe NAO e declarada aqui: ela vem da anotacao do .proto, lida por reflexao.
// E o que torna este teste uma verificacao do sistema e nao da propria montagem —
// se a anotacao do contrato mudasse, este envelope deixaria de ser evento e o
// teste reprovaria, que e exatamente o que se quer.
func envelopeDeEventoDiscreto(sequencia uint64) *contratov1.Envelope {
	return &contratov1.Envelope{
		NumeroDeSequencia: proto.Uint64(sequencia),
		TempoLigadoMs:     proto.Uint64(sequencia * 1000),
		Conteudo: &contratov1.Envelope_MudancaDeEstadoDeMaquina{
			MudancaDeEstadoDeMaquina: &contratov1.MudancaDeEstadoDeMaquina{
				Estado: contratov1.EstadoDeMaquina_ESTADO_DE_MAQUINA_PARADA.Enum(),
			},
		},
	}
}

// TestSaturacaoRespondeComQuatrocentosEVinteENoveERetryAfter fecha a pendencia que
// a V2.1 e a V2.3 registraram nas duas.
//
// O mapeador de status ja devolvia 429 desde a V2.0, e o no ja sabia trata-lo. O
// que faltava era alguem PRODUZIR a categoria: ate a V2.3 o caminho inteiro estava
// pronto e nunca era exercitado, e um caminho que nunca roda e um caminho que nao
// funciona — a mesma licao que a auditoria da V1.2 cobrou do buffer.
func TestSaturacaoRespondeComQuatrocentosEVinteENoveERetryAfter(t *testing.T) {
	t.Parallel()

	cenario := servidorComPortaria(t, ajustesSaturaveis())

	ocupante, err := cenario.portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("a vaga deveria estar livre: %v", err)
	}
	defer ocupante.Sair()

	resposta := enviar(t, cenario.servidor, remessaSerializada(t, envelopeDeAmostra(1)))

	if resposta.Status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, esperado 429", resposta.Status)
	}
	if resposta.RetryAfter != "3" {
		t.Fatalf("Retry-After = %q, esperado \"3\"", resposta.RetryAfter)
	}

	// A remessa recusada NAO chegou ao diario. Contrapressao que gravasse pela
	// metade seria pior que nenhuma: a origem retransmitiria o lote inteiro e o
	// gateway pagaria duas vezes pelo que ja tem.
	registros, err := cenario.diario.LerAPartirDe(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("leitura do diario falhou: %v", err)
	}
	if len(registros) != 0 {
		t.Fatalf("registros no diario = %d, esperado 0: a remessa foi recusada", len(registros))
	}
}

// TestRetryAfterVemDaMedicaoENaoDeUmValorFixo e o que separa esta versao de um
// limitador de taxa qualquer.
//
// Ate a V2.3 o cabecalho carregaria uma constante de dois segundos escrita no
// codigo — um numero que ninguem mediu e que estaria errado em toda instalacao
// cujo disco nao fosse o da maquina do autor. Aqui ele sai do custo observado das
// gravacoes que ja aconteceram neste gateway, neste disco.
func TestRetryAfterVemDaMedicaoENaoDeUmValorFixo(t *testing.T) {
	t.Parallel()

	cenario := servidorComPortaria(t, ajustesSaturaveis())

	// Uma passagem completa de quatro segundos ensina o custo a portaria.
	medida, err := cenario.portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("primeira admissao falhou: %v", err)
	}
	cenario.relogio.Avancar(4 * time.Second)
	medida.Sair()

	ocupante, err := cenario.portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("a vaga deveria ter sido liberada: %v", err)
	}
	defer ocupante.Sair()

	resposta := enviar(t, cenario.servidor, remessaSerializada(t, envelopeDeAmostra(1)))

	// Quatro, e nao o piso de tres: a espera pedida cresceu porque a gravacao
	// medida demorou, e e isso que a origem passa a obedecer.
	if resposta.RetryAfter != "4" {
		t.Fatalf("Retry-After = %q, esperado \"4\" — a espera deveria vir do custo medido",
			resposta.RetryAfter)
	}
}

// TestEventoDiscretoEsperaOndeAAmostraERecusada e a propriedade central da V2.4,
// exercitada de ponta a ponta por HTTP de verdade.
//
// As duas remessas encontram a MESMA fila e recebem respostas opostas, e a
// diferenca nao esta no tamanho nem na ordem de chegada: esta na garantia que a
// classe do conteudo exige. Amostra prefere ser recusada a esperar, porque a
// proxima repoe quase a mesma informacao. Evento discreto prefere esperar, porque
// nao existe proximo que o reponha.
//
// Sem esta distincao a contrapressao seria um limitador de taxa — e um limitador
// de taxa recusa uma parada de maquina com a mesma naturalidade com que recusa a
// milesima leitura de temperatura.
func TestEventoDiscretoEsperaOndeAAmostraERecusada(t *testing.T) {
	t.Parallel()

	ajustes := ajustesSaturaveis()
	// Com fila, a recusa passa a ser decidida pelo ORCAMENTO DE ESPERA, que e onde
	// as duas urgencias divergem — e nao pelo teto de memoria, que vale para as duas.
	ajustes.FilaMaxima = 8

	cenario := servidorComPortaria(t, ajustes)

	// Cinco segundos: acima do orcamento comum (2 s) e abaixo do reservado (10 s).
	medida, err := cenario.portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("primeira admissao falhou: %v", err)
	}
	cenario.relogio.Avancar(5 * time.Second)
	medida.Sair()

	ocupante, err := cenario.portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("a vaga deveria ter sido liberada: %v", err)
	}

	amostra := enviar(t, cenario.servidor, remessaSerializada(t, envelopeDeAmostra(1)))
	if amostra.Status != http.StatusTooManyRequests {
		t.Fatalf("status da amostra = %d, esperado 429", amostra.Status)
	}

	// O evento nao e recusado: ele ENTRA NA FILA. Liberar a vaga o deixa passar.
	evento := make(chan respostaDoIngresso, 1)
	go func() {
		evento <- enviar(t, cenario.servidor, remessaSerializada(t, envelopeDeEventoDiscreto(2)))
	}()

	esperarNaFilaDoIngresso(t, cenario.portaria, 1)
	ocupante.Sair()

	if resposta := <-evento; resposta.Status != http.StatusOK {
		t.Fatalf("status do evento discreto = %d, esperado 200: ele deveria esperar, nao ser recusado",
			resposta.Status)
	}

	estado := cenario.portaria.Estado()
	if estado.RecusadasComuns != 1 || estado.RecusadasReservadas != 0 {
		t.Fatalf("recusas = %d comuns e %d reservadas, esperado 1 e 0",
			estado.RecusadasComuns, estado.RecusadasReservadas)
	}
}

// TestLoteMistoValePeloItemMaisUrgente trava a decisao sobre remessa heterogenea.
//
// A remessa e admitida ou recusada por INTEIRO — nao ha como aceitar metade dela.
// Recusar um lote que carrega uma parada de maquina porque ele tambem carregava
// noventa e nove amostras trocaria a garantia mais forte do sistema pela mais
// fraca, e a origem so descobriria isso quando a contagem de paradas viesse errada.
func TestLoteMistoValePeloItemMaisUrgente(t *testing.T) {
	t.Parallel()

	ajustes := ajustesSaturaveis()
	ajustes.FilaMaxima = 8

	cenario := servidorComPortaria(t, ajustes)

	medida, err := cenario.portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("primeira admissao falhou: %v", err)
	}
	cenario.relogio.Avancar(5 * time.Second)
	medida.Sair()

	ocupante, err := cenario.portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("a vaga deveria ter sido liberada: %v", err)
	}

	misto := make(chan respostaDoIngresso, 1)
	go func() {
		misto <- enviar(t, cenario.servidor, remessaSerializada(t,
			envelopeDeAmostra(1), envelopeDeAmostra(2), envelopeDeEventoDiscreto(3)))
	}()

	esperarNaFilaDoIngresso(t, cenario.portaria, 1)
	ocupante.Sair()

	if resposta := <-misto; resposta.Status != http.StatusOK {
		t.Fatalf("status do lote misto = %d, esperado 200: uma amostra a mais nao pode "+
			"rebaixar a garantia do evento que viajava junto", resposta.Status)
	}
}

// TestPortariaComFolgaNaoAlteraOCaminhoFeliz trava a propriedade mais facil de
// perder de vista.
//
// A contrapressao e um mecanismo que so deve aparecer sob saturacao. Se ela
// mudasse qualquer coisa na operacao normal — status, corpo, cabecalho, ou o que
// chega ao diario —, ela teria custo permanente para pagar um beneficio raro.
func TestPortariaComFolgaNaoAlteraOCaminhoFeliz(t *testing.T) {
	t.Parallel()

	cenario := servidorComPortaria(t, contrapressao.AjustesPadrao())

	resposta := enviar(t, cenario.servidor, remessaSerializada(t, envelopeDeAmostra(1)))
	if resposta.Status != http.StatusOK {
		t.Fatalf("status = %d, esperado 200", resposta.Status)
	}
	if resposta.RetryAfter != "" {
		t.Fatalf("Retry-After = %q, esperado ausente numa resposta aceita", resposta.RetryAfter)
	}
	if estado := cenario.portaria.Estado(); !estado.Admitindo || estado.EmCurso != 0 {
		t.Fatalf("a portaria deveria estar livre apos a remessa: %+v", estado)
	}
}

// esperarNaFilaDoIngresso bloqueia ate a portaria relatar a fila esperada.
//
// Sondagem, e nao um sleep fixo: um sleep escolhido "com folga" e lento no caso bom
// e insuficiente numa maquina carregada — que e como um teste concorrente vira
// intermitente e acaba desligado por quem cansou de reexecutar.
func esperarNaFilaDoIngresso(t *testing.T, portaria *contrapressao.Portaria, esperado int) {
	t.Helper()

	prazo := time.Now().Add(5 * time.Second)
	for time.Now().Before(prazo) {
		if portaria.Estado().Aguardando == esperado {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("aguardando = %d, esperado %d", portaria.Estado().Aguardando, esperado)
}

// TestOsPadroesDosDoisLadosConcordam fecha a unica divergencia possivel entre o
// dominio e a plataforma.
//
// Os mesmos numeros existem em dois lugares por necessidade: instalacao.AdmissaoPadrao
// e a fonte autoritativa, porque a politica e uma afirmacao sobre o dado; e
// contrapressao.AjustesPadrao existe para que aquele package seja exercitavel sozinho,
// sem importar dominio.
//
// Dois conjuntos do mesmo valor divergem, e o que ninguem lembra de atualizar e
// sempre o que esta mais longe do arquivo de configuracao. A garantia contra isso nao
// e um comentario pedindo cuidado — e este teste, que reprova o build no dia em que
// alguem mudar um lado só.
func TestOsPadroesDosDoisLadosConcordam(t *testing.T) {
	t.Parallel()

	doDominio := ingressohttp.AjustesDaPortaria(instalacao.AdmissaoPadrao())
	daPlataforma := contrapressao.AjustesPadrao()

	if doDominio != daPlataforma {
		t.Fatalf("os padroes divergiram:\n  dominio    = %+v\n  plataforma = %+v",
			doDominio, daPlataforma)
	}
}

// TestOrcamentoDaInstalacaoChegaAPortaria trava o caminho inteiro, do arquivo ate a
// recusa.
//
// Nao basta o YAML ser lido: o numero precisa ATRAVESSAR o dominio, o tradutor e a
// portaria, e mudar de fato o instante em que a amostra passa a ser recusada. Um
// campo que carrega e nao surte efeito e pior que um campo que nao existe.
func TestOrcamentoDaInstalacaoChegaAPortaria(t *testing.T) {
	t.Parallel()

	declarada := instalacao.Admissao{
		OrcamentoDaAmostra:        800 * time.Millisecond,
		OrcamentoDoEventoDiscreto: 4 * time.Second,
		FilaMaxima:                256,
	}

	cenario := servidorComPortaria(t, ingressohttp.AjustesDaPortaria(declarada))

	// Um segundo de custo medido: abaixo do orcamento padrao de 2 s — que aceitaria
	// esta remessa — e acima dos 800 ms que a instalacao declarou.
	medida, err := cenario.portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("primeira admissao falhou: %v", err)
	}
	cenario.relogio.Avancar(time.Second)
	medida.Sair()

	ocupante, err := cenario.portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("a vaga deveria ter sido liberada: %v", err)
	}
	defer ocupante.Sair()

	resposta := enviar(t, cenario.servidor, remessaSerializada(t, envelopeDeAmostra(1)))
	if resposta.Status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, esperado 429: a instalacao declarou 800ms e a espera "+
			"estimada e de 1s", resposta.Status)
	}
}

// TestSementeDaCalibracaoNaoSobrescreveMedicaoReal trava a ordem de autoridade.
//
// A semente e um PISO derivado de uma transacao vazia; a media movel vem de
// gravacoes de verdade. Deixar a semente sobrescrever medicao real seria trocar o
// numero bom pelo aproximado — e o momento em que isso aconteceria e justamente
// depois de o gateway ja saber a resposta.
func TestSementeDaCalibracaoNaoSobrescreveMedicaoReal(t *testing.T) {
	t.Parallel()

	cenario := servidorComPortaria(t, contrapressao.AjustesPadrao())

	// Uma gravacao de verdade ensina 3 s.
	medida, err := cenario.portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("admissao falhou: %v", err)
	}
	cenario.relogio.Avancar(3 * time.Second)
	medida.Sair()

	cenario.portaria.Semear(time.Millisecond)

	if custo := cenario.portaria.Estado().CustoMedio; custo != 3*time.Second {
		t.Fatalf("custo medio = %v, esperado 3s: a semente sobrescreveu medicao real", custo)
	}
}

// TestSementeValeQuandoNadaFoiMedidoAinda e o outro lado da mesma regra.
func TestSementeValeQuandoNadaFoiMedidoAinda(t *testing.T) {
	t.Parallel()

	cenario := servidorComPortaria(t, contrapressao.AjustesPadrao())

	cenario.portaria.Semear(7 * time.Millisecond)

	if custo := cenario.portaria.Estado().CustoMedio; custo != 7*time.Millisecond {
		t.Fatalf("custo medio = %v, esperado 7ms", custo)
	}
	// E semear duas vezes nao acumula: a segunda ja encontra medicao registrada.
	cenario.portaria.Semear(99 * time.Millisecond)
	if custo := cenario.portaria.Estado().CustoMedio; custo != 7*time.Millisecond {
		t.Fatalf("custo medio = %v apos segunda semeadura, esperado 7ms", custo)
	}
}
