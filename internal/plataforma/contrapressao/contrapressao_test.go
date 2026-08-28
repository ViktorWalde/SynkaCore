package contrapressao_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/contrapressao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/relogio"
)

var instanteDeReferencia = time.Date(2026, time.August, 28, 9, 0, 0, 0, time.UTC)

// ajustesDeTeste sao os padroes com numeros pequenos e legiveis.
//
// A saturacao e provocada movendo um relogio FALSO, e nao esperando de verdade.
// Um teste que exercitasse saturacao real levaria dezenas de segundos por caso, e
// um teste lento e um teste que alguem desliga — que e como uma trava deixa de
// existir sem ninguem decidir remove-la.
func ajustesDeTeste() contrapressao.Ajustes {
	return contrapressao.Ajustes{
		VagasSimultaneas:   1,
		FilaMaxima:         8,
		OrcamentoComum:     2 * time.Second,
		OrcamentoReservado: 10 * time.Second,
		EsperaMinima:       time.Second,
		EsperaMaxima:       30 * time.Second,
	}
}

// comCustoMedidoDe deixa a portaria com um custo medio conhecido.
//
// Uma passagem completa: entra, o relogio avanca, sai. E a unica forma de a
// portaria aprender um custo — ela nao aceita um numero por configuracao, porque
// um custo declarado seria uma estimativa, e o package inteiro existe para
// substituir estimativa por medicao.
func comCustoMedidoDe(t *testing.T, custo time.Duration) (*contrapressao.Portaria, *relogio.Falso) {
	t.Helper()

	r := relogio.NovoFalso(instanteDeReferencia)
	portaria := contrapressao.NovaPortaria(ajustesDeTeste(), r.Decorrido)

	passagem, err := portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("a primeira admissao deveria passar: %v", err)
	}
	r.Avancar(custo)
	passagem.Sair()

	if medido := portaria.Estado().CustoMedio; medido != custo {
		t.Fatalf("custo medio = %v, esperado %v", medido, custo)
	}
	return portaria, r
}

func TestComVagaLivreAdmiteSemDecidirNada(t *testing.T) {
	t.Parallel()

	r := relogio.NovoFalso(instanteDeReferencia)
	portaria := contrapressao.NovaPortaria(ajustesDeTeste(), r.Decorrido)

	passagem, err := portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("admissao com o caminho livre falhou: %v", err)
	}
	defer passagem.Sair()

	if estado := portaria.Estado(); !estado.Admitindo || estado.EmCurso != 1 {
		t.Fatalf("estado inesperado: %+v", estado)
	}
}

// TestSemMedicaoNenhumaAPortariaAdmite trava a decisao de degradar para "admite".
//
// Na partida a portaria nao sabe quanto custa gravar, e a alternativa a admitir
// seria recusar por precaucao — deixando a planta sem aquisicao justamente no
// instante em que a frota inteira reconecta. Nao afirmar nada e melhor que afirmar
// saturacao que ninguem mediu.
func TestSemMedicaoNenhumaAPortariaAdmite(t *testing.T) {
	t.Parallel()

	r := relogio.NovoFalso(instanteDeReferencia)
	portaria := contrapressao.NovaPortaria(ajustesDeTeste(), r.Decorrido)

	primeira, err := portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("primeira admissao falhou: %v", err)
	}
	defer primeira.Sair()

	// A vaga unica ja esta ocupada e nada foi medido: a espera estimada e zero, e
	// zero cabe em qualquer orcamento.
	if estado := portaria.Estado(); !estado.Admitindo {
		t.Fatal("sem custo medido, a portaria deveria admitir em vez de recusar por precaucao")
	}
}

// TestAmostraERecusadaOndeOEventoDiscretoAindaEntra e o teste central da versao.
//
// As duas remessas chegam no MESMO instante, com a MESMA fila a frente, e recebem
// respostas opostas. A diferenca nao esta em quem chegou primeiro nem em quanto
// cada uma pesa: esta na garantia que a classe do conteudo exige.
//
// Sem esta distincao, contrapressao seria apenas um limitador de taxa — e um
// limitador de taxa recusa a parada de maquina com a mesma naturalidade com que
// recusa a milesima leitura de temperatura.
func TestAmostraERecusadaOndeOEventoDiscretoAindaEntra(t *testing.T) {
	t.Parallel()

	// Cinco segundos de custo medio: acima do orcamento comum (2 s) e abaixo do
	// reservado (10 s). E a faixa em que a reserva existe.
	portaria, _ := comCustoMedidoDe(t, 5*time.Second)

	ocupante, err := portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("a vaga deveria estar livre: %v", err)
	}
	// Sair e idempotente, entao o adiamento convive com a saida explicita mais
	// abaixo — que e exatamente a convivencia que trava a goroutine se ele nao for.
	defer ocupante.Sair()

	_, err = portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if !falha.TemCategoria(err, falha.CategoriaRecursoEsgotado) {
		t.Fatalf("a amostra deveria ser recusada por saturacao, veio: %v", err)
	}

	// Mesma fila, mesma espera estimada, resposta oposta.
	comEvento := make(chan error, 1)
	go func() {
		passagem, err := portaria.Entrar(t.Context(), contrapressao.UrgenciaReservada)
		if err == nil {
			passagem.Sair()
		}
		comEvento <- err
	}()

	// O evento nao foi recusado: ele esta ESPERANDO, que e exatamente o que a
	// urgencia reservada compra. Liberar a vaga o deixa passar.
	esperarNaFila(t, portaria, 1)
	ocupante.Sair()

	if err := <-comEvento; err != nil {
		t.Fatalf("o evento discreto deveria esperar e entrar, veio: %v", err)
	}

	estado := portaria.Estado()
	if estado.RecusadasComuns != 1 || estado.RecusadasReservadas != 0 {
		t.Fatalf("contadores de recusa = %d comuns e %d reservadas, esperado 1 e 0",
			estado.RecusadasComuns, estado.RecusadasReservadas)
	}
}

// TestEventoDiscretoTambemERecusadoQuandoOTetoRealEUltrapassado trava que a
// reserva e uma FAIXA, e nao imunidade.
//
// Um evento que nunca fosse recusado transformaria a fila num deposito sem fundo:
// as goroutines esperando acumulariam corpos de remessa em memoria ate o gateway
// morrer — e um gateway morto nao aceita nem amostra nem evento.
func TestEventoDiscretoTambemERecusadoQuandoOTetoRealEUltrapassado(t *testing.T) {
	t.Parallel()

	portaria, _ := comCustoMedidoDe(t, 30*time.Second)

	ocupante, err := portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("a vaga deveria estar livre: %v", err)
	}
	defer ocupante.Sair()

	_, err = portaria.Entrar(t.Context(), contrapressao.UrgenciaReservada)
	if !falha.TemCategoria(err, falha.CategoriaRecursoEsgotado) {
		t.Fatalf("acima do orcamento reservado o evento tambem deveria ser recusado, veio: %v", err)
	}
	if estado := portaria.Estado(); estado.RecusadasReservadas != 1 {
		t.Fatalf("recusas reservadas = %d, esperado 1", estado.RecusadasReservadas)
	}
}

// TestFilaMaximaLimitaQuantosPodemEsperar trava o teto de MEMORIA.
//
// Ele e independente do orcamento de espera: mesmo com custo medido zero — quando
// a portaria nao tem motivo nenhum para recusar por tempo — a fila nao pode
// crescer sem limite, porque cada esperando segura o corpo da remessa vivo.
func TestFilaMaximaLimitaQuantosPodemEsperar(t *testing.T) {
	t.Parallel()

	ajustes := ajustesDeTeste()
	ajustes.FilaMaxima = 2

	r := relogio.NovoFalso(instanteDeReferencia)
	portaria := contrapressao.NovaPortaria(ajustes, r.Decorrido)

	ocupante, err := portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("a vaga deveria estar livre: %v", err)
	}
	defer ocupante.Sair()

	ctx, cancelar := context.WithCancel(t.Context())
	defer cancelar()

	for range ajustes.FilaMaxima {
		go func() {
			passagem, err := portaria.Entrar(ctx, contrapressao.UrgenciaReservada)
			if err == nil {
				passagem.Sair()
			}
		}()
	}
	esperarNaFila(t, portaria, ajustes.FilaMaxima)

	// A fila esta cheia. O proximo e recusado mesmo sendo urgente e mesmo sem
	// nenhuma espera estimada.
	if _, err := portaria.Entrar(t.Context(), contrapressao.UrgenciaReservada); !falha.TemCategoria(
		err, falha.CategoriaRecursoEsgotado) {
		t.Fatalf("com a fila cheia a recusa deveria ser por saturacao, veio: %v", err)
	}
}

// TestCancelamentoNaFilaNaoDeixaAContagemErrada trava um vazamento que so
// apareceria depois.
//
// Uma origem que desiste — desligamento, tempo limite do cliente, cabo puxado —
// sai da fila. Se a contagem nao voltasse, cada desistencia empurraria a portaria
// um passo em direcao a recusar todo mundo, e o gateway acabaria recusando
// remessas por causa de conexoes que nao existem mais.
func TestCancelamentoNaFilaNaoDeixaAContagemErrada(t *testing.T) {
	t.Parallel()

	r := relogio.NovoFalso(instanteDeReferencia)
	portaria := contrapressao.NovaPortaria(ajustesDeTeste(), r.Decorrido)

	ocupante, err := portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("a vaga deveria estar livre: %v", err)
	}
	defer ocupante.Sair()

	ctx, cancelar := context.WithCancel(t.Context())
	desistente := make(chan error, 1)
	go func() {
		_, err := portaria.Entrar(ctx, contrapressao.UrgenciaComum)
		desistente <- err
	}()

	esperarNaFila(t, portaria, 1)
	cancelar()

	err = <-desistente
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("o cancelamento deveria ser preservado na cadeia do erro, veio: %v", err)
	}
	// Indisponivel, e nao RecursoEsgotado: o gateway nao recusou nada — quem
	// desistiu foi o chamador, e a origem deve retransmitir, nao esperar.
	if !falha.TemCategoria(err, falha.CategoriaIndisponivel) {
		t.Fatalf("categoria = %v, esperado indisponivel", falha.CategoriaDe(err))
	}
	esperarNaFila(t, portaria, 0)
}

func TestEsperaSugeridaRespeitaOPisoEOTeto(t *testing.T) {
	t.Parallel()

	// Piso: o custo medido e de milissegundos, e a espera sugerida nao pode ser
	// menor que a resolucao do Retry-After.
	curta, _ := comCustoMedidoDe(t, 10*time.Millisecond)
	if sugerida := curta.EsperaSugerida(); sugerida != ajustesDeTeste().EsperaMinima {
		t.Fatalf("espera sugerida = %v, esperado o piso de %v",
			sugerida, ajustesDeTeste().EsperaMinima)
	}

	// Teto: um custo absurdo nao pode calar a origem por minutos.
	longa, _ := comCustoMedidoDe(t, 10*time.Minute)
	ocupante, err := longa.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("a vaga deveria estar livre: %v", err)
	}
	defer ocupante.Sair()

	if sugerida := longa.EsperaSugerida(); sugerida != ajustesDeTeste().EsperaMaxima {
		t.Fatalf("espera sugerida = %v, esperado o teto de %v",
			sugerida, ajustesDeTeste().EsperaMaxima)
	}
}

// TestSairLiberaAVagaEAlimentaAMedia trava que a media MOVE.
//
// Uma media que nao acompanhasse a mudanca de regime tornaria a portaria
// permanentemente ancorada no primeiro disco que ela viu — e o dia em que o disco
// ficasse lento seria o dia em que ela pararia de recusar.
func TestSairLiberaAVagaEAlimentaAMedia(t *testing.T) {
	t.Parallel()

	portaria, r := comCustoMedidoDe(t, time.Second)

	passagem, err := portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("a vaga deveria ter sido liberada: %v", err)
	}
	r.Avancar(9 * time.Second)
	passagem.Sair()

	// Media movel com peso 8: 1 s + (9 s - 1 s)/8 = 2 s.
	if medido := portaria.Estado().CustoMedio; medido != 2*time.Second {
		t.Fatalf("custo medio = %v, esperado 2s", medido)
	}
	if estado := portaria.Estado(); estado.EmCurso != 0 {
		t.Fatalf("em curso = %d, esperado 0 apos a saida", estado.EmCurso)
	}
}

// TestPassagemZeradaPodeSairSemEfeito trava o contrato de uso.
//
// O chamador escreve `passagem, err := Entrar(...)` seguido de
// `defer passagem.Sair()`. Exigir que ele lembre de nao adiar a saida no caminho
// de erro transformaria uma regra de uso em oportunidade de defeito — e um panico
// no caminho de aquisicao derrubaria a planta por causa de uma remessa recusada.
func TestPassagemZeradaPodeSairSemEfeito(t *testing.T) {
	t.Parallel()

	var passagem *contrapressao.Passagem
	passagem.Sair()

	// E a segunda saida de uma passagem legitima tambem nao pode travar.
	r := relogio.NovoFalso(instanteDeReferencia)
	portaria := contrapressao.NovaPortaria(ajustesDeTeste(), r.Decorrido)

	usada, err := portaria.Entrar(t.Context(), contrapressao.UrgenciaComum)
	if err != nil {
		t.Fatalf("admissao falhou: %v", err)
	}
	usada.Sair()
	usada.Sair()

	if estado := portaria.Estado(); estado.EmCurso != 0 {
		t.Fatalf("em curso = %d, esperado 0: a segunda saida nao pode contabilizar de novo",
			estado.EmCurso)
	}
}

// esperarNaFila bloqueia ate a portaria relatar a quantidade esperada de
// aguardando.
//
// Sondagem, e nao um sleep fixo: um sleep escolhido "com folga" e lento no caso
// bom e insuficiente numa maquina carregada, que e como um teste concorrente vira
// intermitente e acaba desligado.
func esperarNaFila(t *testing.T, portaria *contrapressao.Portaria, esperado int) {
	t.Helper()

	prazo := time.Now().Add(2 * time.Second)
	for time.Now().Before(prazo) {
		if portaria.Estado().Aguardando == esperado {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("aguardando = %d, esperado %d", portaria.Estado().Aguardando, esperado)
}
