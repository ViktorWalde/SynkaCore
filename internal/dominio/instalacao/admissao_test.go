package instalacao_test

import (
	"testing"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/instalacao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

// TestEventoDiscretoNaoPodeTerOrcamentoMenorQueAmostra e a trava central da V2.5.
//
// Com o orcamento do evento MENOR que o da amostra, a reserva se inverte: o gateway
// passaria a recusar parada de maquina antes de recusar leitura de temperatura.
//
// E nada denunciaria. O gateway continuaria aceitando dado e respondendo saudavel,
// enquanto a contagem de paradas ficaria permanentemente errada — o buraco
// silencioso que a ClasseDeDado existe para tornar impossivel. Por isso e erro de
// PARTIDA: descoberto agora custa uma mensagem, descoberto depois custa a serie.
func TestEventoDiscretoNaoPodeTerOrcamentoMenorQueAmostra(t *testing.T) {
	t.Parallel()

	invertida := instalacao.Admissao{
		OrcamentoDaAmostra:        10 * time.Second,
		OrcamentoDoEventoDiscreto: 2 * time.Second,
		FilaMaxima:                1024,
	}

	_, err := instalacao.NovaAdmissao(invertida)
	if !falha.TemCategoria(err, falha.CategoriaEntradaInvalida) {
		t.Fatalf("orcamento invertido deveria ser recusado, veio: %v", err)
	}
}

// TestOrcamentosIguaisSaoAceitos delimita a trava.
//
// Iguais significam "sem reserva": o evento nao ganha faixa nenhuma alem da amostra.
// E uma configuracao pobre, e ainda assim COERENTE — ninguem e recusado antes de
// quem tem menos a perder. Recusa-la seria o gateway impor uma politica que a
// instalacao tem o direito de escolher.
func TestOrcamentosIguaisSaoAceitos(t *testing.T) {
	t.Parallel()

	semReserva := instalacao.Admissao{
		OrcamentoDaAmostra:        3 * time.Second,
		OrcamentoDoEventoDiscreto: 3 * time.Second,
		FilaMaxima:                64,
	}

	if _, err := instalacao.NovaAdmissao(semReserva); err != nil {
		t.Fatalf("orcamentos iguais deveriam ser aceitos: %v", err)
	}
}

func TestAdmissaoRecusaValoresImpossiveis(t *testing.T) {
	t.Parallel()

	valida := instalacao.AdmissaoPadrao()

	casos := map[string]instalacao.Admissao{
		"amostra zerada": {
			OrcamentoDaAmostra:        0,
			OrcamentoDoEventoDiscreto: valida.OrcamentoDoEventoDiscreto,
			FilaMaxima:                valida.FilaMaxima,
		},
		"amostra negativa": {
			OrcamentoDaAmostra:        -time.Second,
			OrcamentoDoEventoDiscreto: valida.OrcamentoDoEventoDiscreto,
			FilaMaxima:                valida.FilaMaxima,
		},
		"evento zerado": {
			OrcamentoDaAmostra:        valida.OrcamentoDaAmostra,
			OrcamentoDoEventoDiscreto: 0,
			FilaMaxima:                valida.FilaMaxima,
		},
		// Fila zero recusaria toda remessa que nao encontrasse vaga livre. E um
		// limite de MEMORIA, e zerar memoria disponivel nao e uma politica.
		"fila zerada": {
			OrcamentoDaAmostra:        valida.OrcamentoDaAmostra,
			OrcamentoDoEventoDiscreto: valida.OrcamentoDoEventoDiscreto,
			FilaMaxima:                0,
		},
	}

	for nome, politica := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			if _, err := instalacao.NovaAdmissao(politica); err == nil {
				t.Fatalf("%s deveria ser recusada", nome)
			}
		})
	}
}

// TestAdmissaoPadraoEValida trava o padrao contra si mesmo.
//
// Os padroes atravessam a mesma validacao que uma instalacao declarada, e nao um
// caminho paralelo. Um padrao que nao passasse na propria regra seria a forma mais
// constrangedora de a regra estar errada.
func TestAdmissaoPadraoEValida(t *testing.T) {
	t.Parallel()

	if _, err := instalacao.NovaAdmissao(instalacao.AdmissaoPadrao()); err != nil {
		t.Fatalf("os padroes nao passam na propria validacao: %v", err)
	}
}
