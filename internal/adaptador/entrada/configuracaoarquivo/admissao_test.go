package configuracaoarquivo_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/entrada/configuracaoarquivo"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/instalacao"
)

// comAdmissao monta uma configuracao minima com o bloco de admissao indicado.
func comAdmissao(bloco string) string {
	return `
instalacao: planta-teste
pontos_de_medicao:
  - dispositivo: camara-01
    canal: 0
    ponto: curtimento.camara-01.temperatura
    grandeza: temperatura
    unidade: Cel
` + bloco
}

// TestSemBlocoDeAdmissaoValemOsPadroes trava o caso comum.
//
// A grande maioria das instalacoes nunca vai pensar no assunto, e deve receber os
// numeros dimensionados pela medicao da V2.3 sem declarar nada. Exigir o bloco
// transformaria um refinamento em pre-requisito de comissionamento.
func TestSemBlocoDeAdmissaoValemOsPadroes(t *testing.T) {
	t.Parallel()

	configurada, err := configuracaoarquivo.Carregar(
		escreverTemporario(t, comAdmissao("")))
	if err != nil {
		t.Fatalf("carregamento falhou: %v", err)
	}

	if configurada.Admissao() != instalacao.AdmissaoPadrao() {
		t.Fatalf("admissao = %+v, esperado o padrao %+v",
			configurada.Admissao(), instalacao.AdmissaoPadrao())
	}
}

func TestBlocoDeAdmissaoCompletoEAplicado(t *testing.T) {
	t.Parallel()

	configurada, err := configuracaoarquivo.Carregar(escreverTemporario(t, comAdmissao(`
admissao:
  espera_maxima_da_amostra: 800ms
  espera_maxima_do_evento: 4s
  fila_maxima: 256
`)))
	if err != nil {
		t.Fatalf("carregamento falhou: %v", err)
	}

	esperada := instalacao.Admissao{
		OrcamentoDaAmostra:        800 * time.Millisecond,
		OrcamentoDoEventoDiscreto: 4 * time.Second,
		FilaMaxima:                256,
	}
	if configurada.Admissao() != esperada {
		t.Fatalf("admissao = %+v, esperado %+v", configurada.Admissao(), esperada)
	}
}

// TestBlocoParcialHerdaOPadraoCampoACampo trava uma conveniencia que evita erro.
//
// Quem quer mexer so no orcamento da amostra nao deveria precisar repetir os outros
// dois — repetir e onde se copia errado o que nao se queria mudar, e o erro fica
// parecendo deliberado para quem revisar o arquivo depois.
func TestBlocoParcialHerdaOPadraoCampoACampo(t *testing.T) {
	t.Parallel()

	configurada, err := configuracaoarquivo.Carregar(escreverTemporario(t, comAdmissao(`
admissao:
  espera_maxima_da_amostra: 5s
`)))
	if err != nil {
		t.Fatalf("carregamento falhou: %v", err)
	}

	admissao := configurada.Admissao()
	padrao := instalacao.AdmissaoPadrao()

	if admissao.OrcamentoDaAmostra != 5*time.Second {
		t.Errorf("orcamento da amostra = %v, esperado 5s", admissao.OrcamentoDaAmostra)
	}
	if admissao.OrcamentoDoEventoDiscreto != padrao.OrcamentoDoEventoDiscreto {
		t.Errorf("orcamento do evento = %v, esperado o padrao %v",
			admissao.OrcamentoDoEventoDiscreto, padrao.OrcamentoDoEventoDiscreto)
	}
	if admissao.FilaMaxima != padrao.FilaMaxima {
		t.Errorf("fila maxima = %d, esperado o padrao %d", admissao.FilaMaxima, padrao.FilaMaxima)
	}
}

// TestOrcamentoInvertidoDerrubaAPartida verifica que a trava do dominio alcanca o
// arquivo.
//
// A regra mora em instalacao.NovaAdmissao e nao e reimplementada aqui. Este teste
// existe para provar que ela de fato atravessa o carregamento — uma validacao de
// dominio que o adaptador contorna sem querer e uma validacao que nao existe.
func TestOrcamentoInvertidoDerrubaAPartida(t *testing.T) {
	t.Parallel()

	_, err := configuracaoarquivo.Carregar(escreverTemporario(t, comAdmissao(`
admissao:
  espera_maxima_da_amostra: 10s
  espera_maxima_do_evento: 2s
`)))
	if err == nil {
		t.Fatal("orcamento invertido deveria derrubar o carregamento")
	}
	if !strings.Contains(err.Error(), "inverteria a reserva") {
		t.Errorf("a mensagem nao explica a consequencia: %v", err)
	}
}

// TestDuracaoIlegivelDerrubaAPartida trava a diferenca entre ausente e errado.
//
// Campo vazio significa "nao declarei" e cai no padrao. Campo PRESENTE e ilegivel
// significa que alguem quis dizer alguma coisa e o gateway nao entendeu — seguir com
// o padrao ali esconderia justamente o engano que precisa ser visto na partida.
func TestDuracaoIlegivelDerrubaAPartida(t *testing.T) {
	t.Parallel()

	_, err := configuracaoarquivo.Carregar(escreverTemporario(t, comAdmissao(`
admissao:
  espera_maxima_da_amostra: dois segundos
`)))
	if err == nil {
		t.Fatal("duracao ilegivel deveria derrubar o carregamento")
	}
	if !strings.Contains(err.Error(), "espera_maxima_da_amostra") {
		t.Errorf("a mensagem nao diz qual campo esta errado: %v", err)
	}
}

// TestCampoDesconhecidoNaAdmissaoDerrubaAPartida estende a decodificacao estrita ao
// bloco novo.
//
// `espera_maxima_do_eventos` num decodificador tolerante seria ignorado, e o gateway
// subiria com o orcamento padrao enquanto quem escreveu o arquivo estaria convencido
// do contrario — a mesma classe de defeito que `unidad:` produz num ponto de medicao.
func TestCampoDesconhecidoNaAdmissaoDerrubaAPartida(t *testing.T) {
	t.Parallel()

	_, err := configuracaoarquivo.Carregar(escreverTemporario(t, comAdmissao(`
admissao:
  espera_maxima_do_eventos: 4s
`)))
	if err == nil {
		t.Fatal("campo desconhecido no bloco de admissao deveria derrubar o carregamento")
	}
}
