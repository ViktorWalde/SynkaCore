package resiliencia_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/resiliencia"
)

var instanteDeReferencia = time.Date(2026, time.August, 26, 14, 30, 0, 0, time.UTC)

// ajustesRapidos encurta os tempos sem mudar o comportamento.
//
// Os tempos sao injetaveis pelo mesmo motivo da V1.x: com os valores de producao,
// exercitar recuo e recuperacao levaria dezenas de segundos reais — e um teste
// lento e um teste que alguem desliga.
func ajustesRapidos() resiliencia.Ajustes {
	ajustes := resiliencia.AjustesPadrao()
	ajustes.RecuoBase = time.Millisecond
	ajustes.TempoLimitePorTentativa = 200 * time.Millisecond
	ajustes.EsperaAntesDeMeioAbrir = 50 * time.Millisecond
	return ajustes
}

// relogioControlado devolve instantes que o teste move a mao.
type relogioControlado struct{ agora time.Time }

func (r *relogioControlado) Agora() time.Time        { return r.agora }
func (r *relogioControlado) avancar(d time.Duration) { r.agora = r.agora.Add(d) }

var erroDaDependencia = errors.New("banco de consulta fora do ar")

func TestSucessoNaPrimeiraTentativaNaoRecua(t *testing.T) {
	pipeline := resiliencia.NovaPipeline(ajustesRapidos(), nil, nil)
	relogio := &relogioControlado{agora: instanteDeReferencia}

	var chamadas int
	err := pipeline.Executar(t.Context(), relogio.Agora, func(context.Context) error {
		chamadas++
		return nil
	})

	if err != nil {
		t.Fatalf("operacao bem-sucedida nao deveria falhar: %v", err)
	}
	if chamadas != 1 {
		t.Errorf("chamadas = %d, esperado 1", chamadas)
	}
	if pipeline.Estado() != resiliencia.Fechado {
		t.Errorf("estado = %v, esperado Fechado", pipeline.Estado())
	}
}

func TestRetentativaSeEsgotaEClassificaComoIndisponivel(t *testing.T) {
	ajustes := ajustesRapidos()
	pipeline := resiliencia.NovaPipeline(ajustes, nil, nil)
	relogio := &relogioControlado{agora: instanteDeReferencia}

	var chamadas int
	err := pipeline.Executar(t.Context(), relogio.Agora, func(context.Context) error {
		chamadas++
		return erroDaDependencia
	})

	if err == nil {
		t.Fatal("falha persistente deveria propagar")
	}
	if chamadas != ajustes.Tentativas {
		t.Errorf("chamadas = %d, esperado %d", chamadas, ajustes.Tentativas)
	}

	// Indisponivel, e nao Interna: a dependencia caiu, o gateway esta bem. A
	// distincao decide se alguem e acordado.
	if !falha.TemCategoria(err, falha.CategoriaIndisponivel) {
		t.Errorf("categoria = %v, esperado CategoriaIndisponivel", falha.CategoriaDe(err))
	}
	// A causa raiz e preservada para diagnostico.
	if !errors.Is(err, erroDaDependencia) {
		t.Error("a causa original se perdeu no envolvimento do erro")
	}
}

// TestDisjuntorExigeEvidenciaAntesDeAbrir cobre a escolha de TAXA em vez de
// contagem simples.
//
// Contagem simples abriria o disjuntor com amostra minima. Em rede industrial com
// interferencia esporadica, poucas falhas seguidas nao indicam problema sistemico,
// e proteger a dependencia cedo demais transforma instabilidade passageira em
// indisponibilidade real.
func TestDisjuntorExigeEvidenciaAntesDeAbrir(t *testing.T) {
	ajustes := ajustesRapidos()
	ajustes.Tentativas = 1
	pipeline := resiliencia.NovaPipeline(ajustes, nil, nil)
	relogio := &relogioControlado{agora: instanteDeReferencia}

	// Uma unica falha, longe da amostra minima.
	_ = pipeline.Executar(t.Context(), relogio.Agora, func(context.Context) error {
		return erroDaDependencia
	})

	if pipeline.Estado() != resiliencia.Fechado {
		t.Errorf("estado = %v apos uma falha isolada, esperado Fechado", pipeline.Estado())
	}
}

// TestDisjuntorAbreEProtegeADependencia verifica o comportamento que da nome ao
// componente: uma vez aberto, a chamada falha SEM alcancar a dependencia.
func TestDisjuntorAbreEProtegeADependencia(t *testing.T) {
	ajustes := ajustesRapidos()
	ajustes.Tentativas = 1
	pipeline := resiliencia.NovaPipeline(ajustes, nil, nil)
	relogio := &relogioControlado{agora: instanteDeReferencia}

	for range ajustes.ChamadasMinimasNaJanela {
		_ = pipeline.Executar(t.Context(), relogio.Agora, func(context.Context) error {
			return erroDaDependencia
		})
	}

	if pipeline.Estado() != resiliencia.Aberto {
		t.Fatalf("estado = %v apos falhas suficientes, esperado Aberto", pipeline.Estado())
	}

	// A dependencia nao pode mais ser tocada enquanto se recupera.
	var alcancada bool
	err := pipeline.Executar(t.Context(), relogio.Agora, func(context.Context) error {
		alcancada = true
		return nil
	})

	if alcancada {
		t.Error("o disjuntor aberto deixou a chamada alcancar a dependencia")
	}
	if !falha.TemCategoria(err, falha.CategoriaIndisponivel) {
		t.Errorf("categoria = %v, esperado CategoriaIndisponivel", falha.CategoriaDe(err))
	}
}

// TestDisjuntorFechaQuandoADependenciaVolta cobre o ciclo completo de recuperacao,
// incluindo a razao de o meio-aberto NAO consultar a janela.
//
// A janela ainda carrega as falhas que abriram o disjuntor. Se a decisao do
// meio-aberto dependesse dela, o disjuntor continuaria abrindo mesmo com a
// dependencia ja recuperada — e so voltaria sozinho quando as falhas antigas
// saissem da janela, o que pode nunca acontecer se as chamadas estao sendo
// bloqueadas.
func TestDisjuntorFechaQuandoADependenciaVolta(t *testing.T) {
	ajustes := ajustesRapidos()
	ajustes.Tentativas = 1

	var transicoes []string
	observador := func(anterior, atual resiliencia.EstadoDoDisjuntor) {
		transicoes = append(transicoes, anterior.String()+"->"+atual.String())
	}
	pipeline := resiliencia.NovaPipeline(ajustes, observador, nil)
	relogio := &relogioControlado{agora: instanteDeReferencia}

	for range ajustes.ChamadasMinimasNaJanela {
		_ = pipeline.Executar(t.Context(), relogio.Agora, func(context.Context) error {
			return erroDaDependencia
		})
	}
	if pipeline.Estado() != resiliencia.Aberto {
		t.Fatal("o disjuntor deveria ter aberto")
	}

	// Ainda dentro da espera: a prova nao e autorizada.
	relogio.avancar(ajustes.EsperaAntesDeMeioAbrir / 2)
	if err := pipeline.Executar(t.Context(), relogio.Agora, func(context.Context) error {
		return nil
	}); err == nil {
		t.Error("a prova foi autorizada antes de a espera vencer")
	}

	// Espera vencida e dependencia recuperada: uma prova bem-sucedida fecha.
	relogio.avancar(ajustes.EsperaAntesDeMeioAbrir)
	if err := pipeline.Executar(t.Context(), relogio.Agora, func(context.Context) error {
		return nil
	}); err != nil {
		t.Fatalf("a prova deveria passar com a dependencia recuperada: %v", err)
	}

	if pipeline.Estado() != resiliencia.Fechado {
		t.Errorf("estado = %v apos prova bem-sucedida, esperado Fechado", pipeline.Estado())
	}

	esperadas := []string{"closed->open", "open->half_open", "half_open->closed"}
	if len(transicoes) != len(esperadas) {
		t.Fatalf("transicoes = %v, esperado %v", transicoes, esperadas)
	}
	for indice, esperada := range esperadas {
		if transicoes[indice] != esperada {
			t.Errorf("transicoes = %v, esperado %v", transicoes, esperadas)
			break
		}
	}
}

// TestProvaMalSucedidaReabreODisjuntor cobre o outro lado do meio-aberto.
func TestProvaMalSucedidaReabreODisjuntor(t *testing.T) {
	ajustes := ajustesRapidos()
	ajustes.Tentativas = 1
	pipeline := resiliencia.NovaPipeline(ajustes, nil, nil)
	relogio := &relogioControlado{agora: instanteDeReferencia}

	for range ajustes.ChamadasMinimasNaJanela {
		_ = pipeline.Executar(t.Context(), relogio.Agora, func(context.Context) error {
			return erroDaDependencia
		})
	}
	relogio.avancar(2 * ajustes.EsperaAntesDeMeioAbrir)

	_ = pipeline.Executar(t.Context(), relogio.Agora, func(context.Context) error {
		return erroDaDependencia
	})

	if pipeline.Estado() != resiliencia.Aberto {
		t.Errorf("estado = %v apos prova falha, esperado Aberto", pipeline.Estado())
	}
}

// TestTempoLimiteImpedeQueUmaChamadaTraveOCiclo verifica o terceiro estagio.
func TestTempoLimiteImpedeQueUmaChamadaTraveOCiclo(t *testing.T) {
	ajustes := ajustesRapidos()
	ajustes.Tentativas = 1
	ajustes.TempoLimitePorTentativa = 30 * time.Millisecond
	pipeline := resiliencia.NovaPipeline(ajustes, nil, nil)
	relogio := &relogioControlado{agora: instanteDeReferencia}

	inicio := time.Now()
	err := pipeline.Executar(t.Context(), relogio.Agora, func(ctx context.Context) error {
		// Uma dependencia que aceita a chamada e nunca responde.
		<-ctx.Done()
		return ctx.Err()
	})
	decorrido := time.Since(inicio)

	if err == nil {
		t.Fatal("a chamada travada deveria falhar por tempo limite")
	}
	if decorrido > 10*ajustes.TempoLimitePorTentativa {
		t.Errorf("a chamada levou %v, muito alem do tempo limite de %v",
			decorrido, ajustes.TempoLimitePorTentativa)
	}
}

// TestCancelamentoNaoContaComoFalhaDaDependencia protege o disjuntor de um
// desligamento.
//
// Contabilizar o cancelamento abriria o disjuntor por causa de um Ctrl+C, e o
// proximo processo nasceria achando que a dependencia esta fora.
func TestCancelamentoNaoContaComoFalhaDaDependencia(t *testing.T) {
	ajustes := ajustesRapidos()
	ajustes.Tentativas = 3
	pipeline := resiliencia.NovaPipeline(ajustes, nil, nil)
	relogio := &relogioControlado{agora: instanteDeReferencia}

	ctx, cancelar := context.WithCancel(t.Context())
	cancelar()

	var chamadas int
	_ = pipeline.Executar(ctx, relogio.Agora, func(context.Context) error {
		chamadas++
		return context.Canceled
	})

	if chamadas != 1 {
		t.Errorf("chamadas = %d, esperado 1: o cancelamento nao deveria ser retentado", chamadas)
	}
	if pipeline.Estado() != resiliencia.Fechado {
		t.Errorf("estado = %v apos cancelamento, esperado Fechado", pipeline.Estado())
	}
}
