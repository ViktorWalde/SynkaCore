// Package resiliencia implementa a pipeline que protege uma dependencia instavel.
//
// E a reescrita em Go da pipeline Resilience4j da V1.1, e o desenho — disjuntor
// por TAXA de falha, recuo exponencial com jitter, tempo limite por tentativa —
// atravessou a mudanca de linguagem intacto, porque nada nele era sobre Java.
//
// O que MUDOU foi onde ela se aplica, e a mudanca e a mais importante da V2.0.
//
// Na V1.x a pipeline protegia o caminho de ESCRITA: o worker gravava direto no
// banco, e a queda do banco ameacava o dado. Na V2.0 o caminho de aquisicao grava
// no diario local e nunca toca no banco de consulta. Entao esta pipeline protege a
// PROJECAO — e a consequencia e que ela deixou de ser um mecanismo de preservacao
// de dado e virou um mecanismo de qualidade de servico.
//
// Dito de outro jeito: se tudo aqui falhar, os dashboards atrasam. Nada se perde.
package resiliencia

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const operacaoExecutar = "resiliencia.Executar"

// EstadoDoDisjuntor e a situacao corrente do disjuntor.
type EstadoDoDisjuntor uint8

const (
	// Fechado: as chamadas passam normalmente.
	Fechado EstadoDoDisjuntor = iota + 1

	// Aberto: as chamadas falham IMEDIATAMENTE, sem alcancar a dependencia.
	//
	// E o ponto do disjuntor. Sem ele, o mecanismo de retentativa marteleria um
	// banco inacessivel, desperdicando tempo e agravando a sobrecarga justamente
	// durante a recuperacao — que e quando a dependencia menos aguenta carga.
	Aberto

	// MeioAberto: uma chamada de prova esta autorizada, para testar a recuperacao.
	MeioAberto
)

// String devolve o nome estavel do estado.
func (e EstadoDoDisjuntor) String() string {
	switch e {
	case Fechado:
		return "closed"
	case Aberto:
		return "open"
	case MeioAberto:
		return "half_open"
	}
	return "unknown"
}

// Ajustes reune os parametros da pipeline.
//
// Injetaveis, e nao constantes, pelo mesmo motivo da V1.x: sem isso os testes
// levariam dezenas de segundos reais para exercitar recuo e recuperacao, e um
// teste lento e um teste que alguem desliga.
type Ajustes struct {
	// Tentativas e quantas vezes uma operacao e tentada, incluindo a primeira.
	Tentativas int

	// RecuoBase e o intervalo antes da segunda tentativa; ele dobra a cada uma.
	RecuoBase time.Duration

	// FracaoDeJitter espalha as tentativas em torno do recuo calculado.
	//
	// Sem jitter, varias instancias que falharem ao mesmo tempo tentam de novo nos
	// mesmos instantes e a carga volta em picos sincronizados sobre uma dependencia
	// que acabou de se levantar.
	FracaoDeJitter float64

	// TempoLimitePorTentativa impede que uma unica chamada trave o ciclo.
	TempoLimitePorTentativa time.Duration

	// TaxaDeFalhaParaAbrir e a fracao de falhas que abre o disjuntor.
	//
	// TAXA, e nao contagem simples. Contagem simples abre o disjuntor com amostra
	// minima: em rede industrial com interferencia esporadica, tres falhas seguidas
	// nao indicam problema sistemico. Taxa sobre uma janela exige EVIDENCIA antes de
	// proteger a dependencia.
	TaxaDeFalhaParaAbrir float64

	// ChamadasMinimasNaJanela e a amostra minima para a taxa valer.
	ChamadasMinimasNaJanela int

	// TamanhoDaJanela e quantas chamadas recentes entram no calculo da taxa.
	TamanhoDaJanela int

	// EsperaAntesDeMeioAbrir e quanto o disjuntor fica aberto antes de testar.
	EsperaAntesDeMeioAbrir time.Duration
}

// AjustesPadrao devolve os parametros herdados da V1.1, com a justificativa de cada um.
func AjustesPadrao() Ajustes {
	return Ajustes{
		// Tres tentativas com recuo de ~2s, ~4s, ~8s cobrem a instabilidade curta
		// sem prender o ciclo por muito tempo.
		Tentativas:     3,
		RecuoBase:      2 * time.Second,
		FracaoDeJitter: 0.2,

		// Operacao normal de banco leva menos de 100 ms. Cinco segundos e generoso
		// para rede industrial lenta e rigido o bastante para nao travar o ciclo.
		TempoLimitePorTentativa: 5 * time.Second,

		// Metade das gravacoes falhando ja indica problema real. Esperar 100%
		// significaria proteger o banco so depois de ele estar completamente fora.
		TaxaDeFalhaParaAbrir:    0.5,
		ChamadasMinimasNaJanela: 10,
		TamanhoDaJanela:         20,
		EsperaAntesDeMeioAbrir:  30 * time.Second,
	}
}

// Observador e notificado das transicoes, para log e para o estado operacional.
//
// Funcao, e nao interface de varios metodos: so existe um evento que interessa a
// quem observa, e uma interface transformaria isso em cerimonia.
type Observador func(anterior, atual EstadoDoDisjuntor)

// Pipeline compoe disjuntor, retentativa e tempo limite.
//
// A ORDEM e disjuntor -> retentativa -> tempo limite, de fora para dentro, e ela e
// deliberada: o disjuntor supervisiona o conjunto. Se ele esta aberto, a chamada
// falha na hora sem nem alcancar a retentativa. Na ordem inversa, a retentativa
// martelaria a dependencia inacessivel antes de o disjuntor ter chance de proteger.
type Pipeline struct {
	ajustes    Ajustes
	observador Observador
	sortear    func() float64

	mutex       sync.Mutex
	estado      EstadoDoDisjuntor
	resultados  []bool // janela deslizante: true == sucesso
	abertoDesde time.Time
}

// NovaPipeline constroi a pipeline.
func NovaPipeline(ajustes Ajustes, observador Observador, sortear func() float64) *Pipeline {
	if observador == nil {
		observador = func(EstadoDoDisjuntor, EstadoDoDisjuntor) {}
	}
	if sortear == nil {
		sortear = func() float64 { return 0.5 }
	}
	return &Pipeline{
		ajustes:    ajustes,
		observador: observador,
		sortear:    sortear,
		estado:     Fechado,
		resultados: make([]bool, 0, ajustes.TamanhoDaJanela),
	}
}

// Executar roda a operacao sob a pipeline.
//
// agora e injetado para que o teste possa avancar o tempo sem esperar de verdade.
func (p *Pipeline) Executar(ctx context.Context, agora func() time.Time, operacao func(context.Context) error) error {
	if err := p.autorizar(agora()); err != nil {
		return err
	}

	var ultimoErro error
	for tentativa := range p.ajustes.Tentativas {
		if tentativa > 0 {
			if err := p.esperar(ctx, tentativa); err != nil {
				return err
			}
		}

		err := p.executarComTempoLimite(ctx, operacao)
		if err == nil {
			p.registrarResultado(true, agora())
			return nil
		}
		ultimoErro = err

		// Cancelamento nao e falha da dependencia: e desligamento. Contabiliza-lo
		// abriria o disjuntor por causa de um Ctrl+C, e o proximo processo nasceria
		// achando que o banco esta fora.
		if errors.Is(err, context.Canceled) {
			return err
		}
	}

	p.registrarResultado(false, agora())
	return falha.Envolver(falha.CategoriaIndisponivel, operacaoExecutar,
		"operacao falhou apos esgotar as tentativas", ultimoErro)
}

// autorizar decide se a chamada pode prosseguir, dado o estado do disjuntor.
func (p *Pipeline) autorizar(agora time.Time) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.estado != Aberto {
		return nil
	}
	if agora.Sub(p.abertoDesde) < p.ajustes.EsperaAntesDeMeioAbrir {
		return falha.Nova(falha.CategoriaIndisponivel, operacaoExecutar,
			"disjuntor aberto: a dependencia esta protegida enquanto se recupera")
	}

	p.transitar(MeioAberto, agora)
	return nil
}

// executarComTempoLimite aplica o tempo limite de uma tentativa individual.
func (p *Pipeline) executarComTempoLimite(ctx context.Context, operacao func(context.Context) error) error {
	ctxDaTentativa, cancelar := context.WithTimeout(ctx, p.ajustes.TempoLimitePorTentativa)
	defer cancelar()
	return operacao(ctxDaTentativa)
}

// esperar aplica o recuo exponencial com jitter, respeitando o cancelamento.
func (p *Pipeline) esperar(ctx context.Context, tentativa int) error {
	recuo := p.ajustes.RecuoBase
	for range tentativa - 1 {
		recuo *= 2
	}

	deslocamento := (p.sortear()*2 - 1) * p.ajustes.FracaoDeJitter
	comJitter := time.Duration(float64(recuo) * (1 + deslocamento))
	if comJitter < 0 {
		comJitter = 0
	}

	temporizador := time.NewTimer(comJitter)
	defer temporizador.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-temporizador.C:
		return nil
	}
}

// registrarResultado alimenta a janela deslizante e reavalia o disjuntor.
func (p *Pipeline) registrarResultado(sucesso bool, agora time.Time) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// Meio-aberto e decidido pela chamada de PROVA, nao pela taxa da janela: a
	// janela ainda carrega as falhas que abriram o disjuntor, e usa-la manteria o
	// disjuntor aberto mesmo com a dependencia ja recuperada.
	if p.estado == MeioAberto {
		if sucesso {
			p.resultados = p.resultados[:0]
			p.transitar(Fechado, agora)
		} else {
			p.transitar(Aberto, agora)
		}
		return
	}

	p.resultados = append(p.resultados, sucesso)
	if len(p.resultados) > p.ajustes.TamanhoDaJanela {
		p.resultados = p.resultados[1:]
	}

	if len(p.resultados) < p.ajustes.ChamadasMinimasNaJanela {
		return
	}

	var falhas int
	for _, resultado := range p.resultados {
		if !resultado {
			falhas++
		}
	}
	if float64(falhas)/float64(len(p.resultados)) >= p.ajustes.TaxaDeFalhaParaAbrir {
		p.transitar(Aberto, agora)
	}
}

// transitar muda o estado e notifica, apenas quando ha mudanca de verdade. Exige o mutex.
func (p *Pipeline) transitar(destino EstadoDoDisjuntor, agora time.Time) {
	if p.estado == destino {
		return
	}
	anterior := p.estado
	p.estado = destino
	if destino == Aberto {
		p.abertoDesde = agora
	}

	// Notificado com o mutex SEGURO, ao contrario do rastreador de estado
	// operacional. E aceitavel aqui porque o observador desta pipeline apenas
	// repassa a transicao ao rastreador, que tem bloqueio proprio e nao faz I/O; o
	// log acontece la, ja fora deste mutex.
	p.observador(anterior, destino)
}

// Estado devolve a situacao corrente do disjuntor.
func (p *Pipeline) Estado() EstadoDoDisjuntor {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.estado
}
