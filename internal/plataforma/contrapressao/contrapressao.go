// Package contrapressao decide QUEM entra no caminho de gravacao quando ele nao
// da conta de todos, e diz por quanto tempo quem ficou de fora deve esperar.
//
// Ele existe porque a V2.3 mediu o gateway e encontrou uma lacuna que a propria
// medicao tornou visivel: o sistema fazia contrapressao POR LATENCIA. Passado o
// teto, nada quebrava — a fila crescia dentro do pool de conexoes do diario, a
// resposta demorava, e a origem descobria a saturacao pelo tempo de resposta.
//
// Funcionava, e mesmo assim era o desenho errado, por tres razoes:
//
//  1. A fila estava num lugar onde ninguem a media. Ela vivia dentro do
//     database/sql, invisivel ao /saude e a qualquer painel.
//  2. A origem inferia. Tempo de resposta alto pode ser saturacao, rede lenta ou
//     um gateway travado, e as tres pedem coisas diferentes.
//  3. Todo mundo esperava igual. Uma amostra de temperatura e uma parada de
//     maquina ficavam na mesma fila, pela mesma duracao — apagando exatamente a
//     distincao de que sai toda a politica do sistema.
//
// A INVERSAO QUE ESTE PACKAGE IMPLEMENTA e a terceira, e ela e a razao de ele
// existir separado de um limitador de taxa qualquer:
//
//	amostra          — prefere ser RECUSADA a esperar. A proxima chega logo e
//	                   carrega quase a mesma informacao; uma amostra entregue com
//	                   dez segundos de atraso nao vale mais que a que vem atras
//	                   dela, e ocupar a fila com ela atrasa o que nao tem
//	                   substituto.
//	evento discreto  — prefere ESPERAR a ser recusado. Ele nao tem vizinho que o
//	                   substitua, e recusa-lo significa empurrar para a origem uma
//	                   retransmissao que pode nunca acontecer se ela reiniciar.
//
// Recusar nao perde dado: a origem devolve o lote ao buffer e retransmite. E por
// isso que recusar cedo e barato para amostra e caro para evento — e por isso que
// as duas classes tem ORCAMENTOS DE ESPERA diferentes aqui, em vez de um limite
// unico que estaria errado nos dois sentidos ao mesmo tempo.
//
// Este package nao conhece HTTP, dominio nem classe de dado. Ele fala de urgencia,
// e quem traduz classe em urgencia e o adaptador de ingresso — pela mesma regra
// que faz o adaptador, e nao o dominio, traduzir falha.Categoria em status HTTP.
package contrapressao

import (
	"context"
	"sync"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const operacaoEntrar = "contrapressao.Entrar"

// Urgencia declara o que o chamador prefere quando o caminho esta congestionado.
//
// Duas, e nao um numero de prioridade. Prioridade numerica convida a inventar
// niveis intermediarios que ninguem sabe justificar; aqui as duas saem das duas
// classes de dado do contrato, e uma terceira so pode existir se uma terceira
// classe existir.
type Urgencia uint8

const (
	// UrgenciaComum prefere ser recusada a esperar muito.
	UrgenciaComum Urgencia = iota + 1

	// UrgenciaReservada prefere esperar a ser recusada, e por isso alcanca uma
	// faixa de espera que a comum nao alcanca.
	UrgenciaReservada
)

// String devolve o nome estavel da urgencia, usado em log e no health check.
// Ingles por convencao de saida, como falha.Categoria.String.
func (u Urgencia) String() string {
	switch u {
	case UrgenciaComum:
		return "common"
	case UrgenciaReservada:
		return "reserved"
	}
	return "unknown"
}

// Ajustes reune os parametros da portaria.
//
// Injetaveis, e nao constantes embutidas: uma planta menor roda o mesmo binario
// com outros numeros, e o teste precisa exercitar saturacao sem saturar de verdade.
type Ajustes struct {
	// VagasSimultaneas e quantos chamadores podem estar gravando ao mesmo tempo.
	//
	// Um, por padrao, porque o diario TEM um escritor so — SetMaxOpenConns(1) — e
	// a V2.3 mediu que a latencia cresce linearmente com o numero de origens, que e
	// a assinatura de escritor serializado. Admitir mais que isso nao produziria
	// paralelismo: produziria a mesma fila, de volta ao lugar onde ninguem a mede.
	VagasSimultaneas int

	// FilaMaxima limita quantos podem ESPERAR, e existe por memoria, nao por
	// politica.
	//
	// Cada esperando e uma goroutine com o corpo da remessa vivo na mao. Sem teto,
	// uma saturacao prolongada troca "resposta lenta" por "gateway morto por falta
	// de memoria" — e esse e o unico modo de falha que a aquisicao nao sobrevive.
	// Dimensionado com folga para nunca ser o limite que morde primeiro: quem
	// decide a recusa no uso normal e o orcamento de espera.
	FilaMaxima int

	// OrcamentoComum e quanto uma remessa de urgencia comum aceita esperar.
	OrcamentoComum time.Duration

	// OrcamentoReservado e quanto uma remessa de urgencia reservada aceita esperar.
	//
	// Maior que o comum de proposito, e a diferenca entre os dois E a reserva. Nao
	// ha um contador de vagas guardadas: a reserva e temporal, e nao espacial —
	// quando a fila passa do orcamento comum, as vagas que continuam sendo
	// distribuidas vao so para quem carrega evento discreto.
	OrcamentoReservado time.Duration

	// EsperaMinima e o piso do Retry-After devolvido a origem.
	//
	// Existe porque Retry-After tem resolucao de SEGUNDOS. Uma estimativa de 300 ms
	// arredondada para baixo viraria zero, e a origem voltaria imediatamente — o
	// recuo deixaria de recuar exatamente quando ele e necessario.
	EsperaMinima time.Duration

	// EsperaMaxima e o teto do Retry-After.
	//
	// Uma estimativa disparada nao pode calar a frota por minutos: quando o gateway
	// se recuperar, a origem precisa perceber em segundos.
	EsperaMaxima time.Duration
}

// AjustesPadrao devolve os parametros dimensionados a partir da medicao da V2.3.
//
// Os orcamentos sao escolhidos para que o regime que a V2.3 mediu como saudavel
// NAO sofra recusa. Naquela rodada, 200 origens entregando 100 envelopes cada
// custaram ~33 us por envelope, ou ~3,3 ms por remessa: a fila teria de passar de
// seiscentos esperando para a estimativa alcancar dois segundos, e com 200 origens
// ela nao passa de 200. Em 400 origens, onde a medicao registrou p99 de 5,6 s, ela
// passa — que e exatamente o regime que a V2.3 descreveu como "acima do teto".
//
// Dito de outro jeito: estes numeros nao foram escolhidos para parecer prudentes.
// Eles foram escolhidos para que a recusa comece onde a medicao mostrou que o
// sistema para de dar conta, e nao antes.
func AjustesPadrao() Ajustes {
	return Ajustes{
		VagasSimultaneas:   1,
		FilaMaxima:         1024,
		OrcamentoComum:     2 * time.Second,
		OrcamentoReservado: 10 * time.Second,
		EsperaMinima:       time.Second,
		EsperaMaxima:       30 * time.Second,
	}
}

// pesoDaAmostraNoCustoMedio e o divisor da media movel exponencial do custo.
//
// Oito da a media uma memoria de aproximadamente oito gravacoes: rapida o
// bastante para acompanhar uma mudanca de regime — um disco que ficou lento, um
// lote que dobrou de tamanho — e lenta o bastante para nao transformar uma unica
// gravacao atipica em recusa geral.
const pesoDaAmostraNoCustoMedio = 8

// Portaria admite ou recusa a entrada no caminho de gravacao.
//
// Ela nao mede o disco nem a CPU. Ela mede QUANTO TEMPO cada admitido levou lá
// dentro, e usa isso para responder a unica pergunta que interessa a quem esta na
// porta: quanto tempo eu esperaria se entrasse na fila agora.
//
// Medir o efeito em vez da causa e deliberado. Uma medida de CPU ou de fila de
// disco exigiria acertar a traducao entre aquele numero e a latencia percebida, e
// erraria sozinha no dia em que o gargalo mudasse de lugar. O tempo gasto lá
// dentro ja e a resposta, qualquer que seja a causa.
type Portaria struct {
	ajustes Ajustes

	// decorrido devolve a leitura MONOTONICA. Nunca o relogio de parede: um acerto
	// de hora no meio de uma gravacao produziria um custo negativo ou gigante, e a
	// portaria passaria a recusar tudo por causa do NTP.
	decorrido func() time.Duration

	// vagas e o semaforo. Enviar ocupa uma vaga; receber a devolve.
	vagas chan struct{}

	mutex               sync.Mutex
	aguardando          int
	emCurso             int
	custoMedio          time.Duration
	houveMedicao        bool
	recusadasComuns     uint64
	recusadasReservadas uint64
}

// NovaPortaria constroi a portaria.
//
// decorrido e injetado, e nao lido de time.Now, para que o teste possa exercitar
// saturacao sem saturar de verdade: fabricar um custo medio de cinco segundos e
// mover um relogio falso, e nao esperar cinco segundos.
func NovaPortaria(ajustes Ajustes, decorrido func() time.Duration) *Portaria {
	if ajustes.VagasSimultaneas < 1 {
		ajustes.VagasSimultaneas = 1
	}
	if ajustes.FilaMaxima < 0 {
		ajustes.FilaMaxima = 0
	}
	if decorrido == nil {
		// Sem relogio nao ha custo medido, e sem custo medido a portaria admite
		// todo mundo. Isso e um defeito de composicao, nao um modo de operacao —
		// mas degradar para "admite" e o unico erro barato aqui: a alternativa
		// seria recusar aquisicao por causa de um parametro esquecido.
		decorrido = func() time.Duration { return 0 }
	}
	return &Portaria{
		ajustes:   ajustes,
		decorrido: decorrido,
		vagas:     make(chan struct{}, ajustes.VagasSimultaneas),
	}
}

// Semear informa um custo inicial, medido antes de qualquer remessa real.
//
// Existe porque a portaria nasce SEM saber quanto custa gravar neste disco, e
// enquanto ela nao sabe a espera estimada e zero — cabe em qualquer orcamento, e
// todo mundo entra. A degradacao e segura e vale no pior momento possivel: logo
// apos um reinicio do gateway, quando a frota inteira reconecta com o buffer cheio.
//
// SO SEMEIA SE NADA FOI MEDIDO AINDA, e essa condicao e o ponto. A semente e um
// piso derivado de uma transacao vazia, e nao uma previsao do custo de uma remessa
// de verdade; deixa-la sobrescrever medicao real seria trocar o numero bom pelo
// aproximado. Ela preenche a lacuna inicial e depois se cala — a media movel,
// alimentada por gravacoes de verdade, corrige para cima em poucas remessas.
//
// Custo nao positivo e ignorado: uma calibracao que nao mediu nada nao tem o que
// afirmar, e afirmar zero seria voltar ao estado que este metodo existe para
// fechar.
func (p *Portaria) Semear(custo time.Duration) {
	if custo <= 0 {
		return
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.houveMedicao {
		return
	}
	p.custoMedio = custo
	p.houveMedicao = true
}

// Passagem e a prova de que o portador foi admitido, e o unico jeito de devolver
// a vaga.
//
// Sem construtor exportado, de proposito: nao existe Passagem que nao tenha vindo
// de uma admissao real, do mesmo jeito que nao existe Envelope que nao tenha
// passado por NovoEnvelope.
type Passagem struct {
	portaria *Portaria
	entrada  time.Duration
}

// Sair devolve a vaga e contabiliza quanto o portador levou lá dentro.
//
// IDEMPOTENTE, e a segunda chamada e a que importa. A primeira versao devolvia a
// passagem por VALOR, e sair duas vezes tentava devolver uma vaga que ja tinha
// voltado — travando aquela goroutine para sempre. O proprio teste desta versao
// caiu nisso antes de qualquer origem cair, e o defeito e do tipo que so aparece
// em producao sob um caminho de erro raro: um `defer passagem.Sair()` convivendo
// com uma saida antecipada, escrito meses depois por quem nao lembra da regra.
//
// Um tipo que exige ser usado exatamente uma vez, e que pendura o processo quando
// nao e, transfere ao chamador uma obrigacao que ele nao tem como verificar.
// Ponteiro anulado na saida resolve isso na definicao, e nao na disciplina.
//
// Chamar em Passagem nula ou ja usada nao faz nada. Isso importa porque o caminho
// do chamador e `passagem, err := Entrar(...)` seguido de `defer passagem.Sair()`:
// exigir que ele lembre de nao adiar a saida quando houve erro seria trocar uma
// regra de uso por uma oportunidade de defeito.
func (p *Passagem) Sair() {
	if p == nil || p.portaria == nil {
		return
	}
	portaria := p.portaria
	p.portaria = nil
	portaria.sair(p.entrada)
}

// Entrar admite ou recusa o chamador, esperando quando preciso.
//
// A DECISAO ACONTECE ANTES DA ESPERA, e nao depois de um tempo limite. Um tempo
// limite recusaria a remessa DEPOIS de ja ter ocupado a fila pelo tempo inteiro —
// pagando o congestionamento e ainda assim nao entregando o dado. Aqui a recusa e
// imediata e barata, e o que ela devolve a origem e a informacao que ela nao
// tinha: quanto tempo esperar.
func (p *Portaria) Entrar(ctx context.Context, urgencia Urgencia) (*Passagem, error) {
	p.mutex.Lock()
	if !p.admitiriaBloqueado(urgencia) {
		espera := p.esperaEstimadaBloqueada()
		p.contabilizarRecusaBloqueada(urgencia)
		p.mutex.Unlock()
		return nil, falha.Nova(falha.CategoriaRecursoEsgotado, operacaoEntrar,
			"gateway saturado: a espera estimada ("+espera.String()+
				") excede o orcamento da urgencia "+urgencia.String())
	}
	p.aguardando++
	p.mutex.Unlock()

	select {
	case p.vagas <- struct{}{}:
	case <-ctx.Done():
		// A origem desistiu ou desligou a conexao. A vaga nunca foi ocupada, e o
		// unico dever aqui e nao deixar a fila contando um esperando que ja foi
		// embora — uma contagem que so cresce faria a portaria recusar todo mundo
		// depois de algumas desistencias.
		p.mutex.Lock()
		p.aguardando--
		p.mutex.Unlock()
		return nil, falha.Envolver(falha.CategoriaIndisponivel, operacaoEntrar,
			"a requisicao foi cancelada antes da admissao", ctx.Err())
	}

	p.mutex.Lock()
	p.aguardando--
	p.emCurso++
	p.mutex.Unlock()

	return &Passagem{portaria: p, entrada: p.decorrido()}, nil
}

// sair devolve a vaga e alimenta a media movel do custo.
func (p *Portaria) sair(entrada time.Duration) {
	custo := p.decorrido() - entrada

	p.mutex.Lock()
	p.emCurso--
	// Custo negativo so e alcancavel com um relogio que anda para tras, o que a
	// leitura monotonica nao faz. Ignora-lo em vez de contabiliza-lo mantem a media
	// intacta caso a injecao do relogio esteja errada.
	if custo >= 0 {
		p.registrarCustoBloqueado(custo)
	}
	p.mutex.Unlock()

	<-p.vagas
}

// registrarCustoBloqueado atualiza a media movel exponencial. Exige o mutex.
//
// A primeira medicao ENTRA INTEIRA, em vez de ser diluida contra um zero inicial.
// Semear com zero faria a portaria julgar as primeiras dezenas de remessas com um
// custo que ela sabe estar errado, e admitir demais justamente na partida — que e
// quando uma frota inteira reconecta ao mesmo tempo.
func (p *Portaria) registrarCustoBloqueado(custo time.Duration) {
	if !p.houveMedicao {
		p.custoMedio = custo
		p.houveMedicao = true
		return
	}
	p.custoMedio += (custo - p.custoMedio) / pesoDaAmostraNoCustoMedio
}

// esperaEstimadaBloqueada devolve quanto esperaria quem entrasse na fila agora.
// Exige o mutex.
//
// Quem esta na frente e a soma de quem espera com quem ja esta dentro, e cada um
// custa uma passagem media pelo caminho de gravacao. Dividido pelas vagas, porque
// com mais de uma vaga a fila drena em paralelo.
func (p *Portaria) esperaEstimadaBloqueada() time.Duration {
	if !p.houveMedicao {
		// Sem nenhuma gravacao concluida nao ha o que estimar, e afirmar zero e
		// mais honesto que chutar: a portaria admite ate ter medido alguma coisa.
		return 0
	}
	adiante := p.aguardando + p.emCurso
	return time.Duration(adiante) * p.custoMedio / time.Duration(p.ajustes.VagasSimultaneas)
}

// admitiriaBloqueado informa se um chamador desta urgencia entraria agora.
// Exige o mutex.
//
// UMA funcao, usada tanto por Entrar quanto pelo relatorio de saude. Duas copias
// da mesma condicao divergiriam, e a que ninguem lembra de atualizar e sempre a do
// relatorio — produzindo um /saude que diz "aceitando" enquanto o gateway recusa.
func (p *Portaria) admitiriaBloqueado(urgencia Urgencia) bool {
	if p.aguardando+p.emCurso < p.ajustes.VagasSimultaneas {
		// Ha vaga livre agora. Nao ha espera a orcar, e portanto nada a decidir.
		return true
	}
	if p.aguardando >= p.ajustes.FilaMaxima {
		return false
	}
	return p.esperaEstimadaBloqueada() <= p.orcamentoDe(urgencia)
}

// orcamentoDe devolve quanto esta urgencia aceita esperar.
//
// Sem clausula default: o linter exhaustive cobra uma decisao explicita para toda
// urgencia nova. Uma urgencia que caisse no padrao herdaria um orcamento que
// ninguem escolheu para ela — e aqui a escolha errada custa dado recusado ou fila
// crescendo sem limite.
func (p *Portaria) orcamentoDe(urgencia Urgencia) time.Duration {
	switch urgencia {
	case UrgenciaComum:
		return p.ajustes.OrcamentoComum
	case UrgenciaReservada:
		return p.ajustes.OrcamentoReservado
	}
	// Urgencia invalida recebe o orcamento maior. Errar para o lado de preservar
	// dado e sempre o erro mais barato.
	return p.ajustes.OrcamentoReservado
}

// contabilizarRecusaBloqueada separa as recusas por urgencia. Exige o mutex.
//
// Dois contadores, e nao um total, porque as duas recusas significam coisas
// diferentes e pedem respostas diferentes. Recusar amostra e a POLITICA
// FUNCIONANDO: o sistema esta protegendo o que nao tem substituto. Recusar evento
// discreto e o TETO REAL sendo ultrapassado, e ninguem deveria descobrir isso
// somado ao numero que sobe todo dia.
func (p *Portaria) contabilizarRecusaBloqueada(urgencia Urgencia) {
	switch urgencia {
	case UrgenciaComum:
		p.recusadasComuns++
	case UrgenciaReservada:
		p.recusadasReservadas++
	}
}

// EsperaSugerida devolve quanto o gateway pede que a origem aguarde.
//
// E a estimativa medida, limitada ao piso e ao teto. Ela e o conteudo do
// Retry-After, e a razao de a origem parar de adivinhar: recuo exponencial existe
// porque quem recua nao tem informacao nenhuma, e aqui quem responde tem.
func (p *Portaria) EsperaSugerida() time.Duration {
	p.mutex.Lock()
	espera := p.esperaEstimadaBloqueada()
	p.mutex.Unlock()

	if espera < p.ajustes.EsperaMinima {
		return p.ajustes.EsperaMinima
	}
	if espera > p.ajustes.EsperaMaxima {
		return p.ajustes.EsperaMaxima
	}
	return espera
}

// Estado e o retrato da portaria, para o health check e para o log.
type Estado struct {
	// Admitindo informa se uma remessa de urgencia comum entraria agora.
	//
	// E a linha que responde "o gateway esta recusando?" sem que ninguem precise
	// comparar contadores entre duas consultas.
	Admitindo bool

	Aguardando int
	EmCurso    int

	// CustoMedio e quanto uma remessa tem levado no caminho de gravacao.
	CustoMedio time.Duration

	// EsperaEstimada e quanto esperaria quem chegasse agora.
	EsperaEstimada time.Duration

	RecusadasComuns     uint64
	RecusadasReservadas uint64
}

// Estado devolve o retrato corrente, lido como um conjunto consistente.
//
// Sob um bloqueio so, e nao campo a campo: com leituras independentes, um
// observador poderia relatar fila vazia com espera estimada alta, e ninguem
// conseguiria interpretar o resultado.
func (p *Portaria) Estado() Estado {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	return Estado{
		Admitindo:           p.admitiriaBloqueado(UrgenciaComum),
		Aguardando:          p.aguardando,
		EmCurso:             p.emCurso,
		CustoMedio:          p.custoMedio,
		EsperaEstimada:      p.esperaEstimadaBloqueada(),
		RecusadasComuns:     p.recusadasComuns,
		RecusadasReservadas: p.recusadasReservadas,
	}
}
