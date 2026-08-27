package aquisicao_test

import (
	"testing"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
)

// TestEventoDiscretoTemFolgaDeLatenciaSobreAmostra protege a razao de as duas
// classes existirem.
//
// Se o orcamento de latencia do evento discreto nao for ESTRITAMENTE menor que o
// da amostra, o lote de telemetria arrasta o alarme junto e a distincao entre as
// classes vira decorativa: o operador so ve a parada depois que a janela da
// telemetria fechar.
func TestEventoDiscretoTemFolgaDeLatenciaSobreAmostra(t *testing.T) {
	daAmostra := aquisicao.ClasseAmostra.LatenciaMaximaDeEntrega()
	doEvento := aquisicao.ClasseEventoDiscreto.LatenciaMaximaDeEntrega()

	if doEvento >= daAmostra {
		t.Errorf("latencia do evento discreto (%v) precisa ser menor que a da amostra (%v)",
			doEvento, daAmostra)
	}
}

// TestCadaClasseDeclaraAsCincoPoliticas garante que nenhuma classe fica com uma
// politica indefinida.
//
// Uma politica no valor zero significa "ninguem decidiu", e o lugar onde isso
// aparece e o dia da falha — quando a pergunta "esse dado podia ser descartado?"
// finalmente importa.
func TestCadaClasseDeclaraAsCincoPoliticas(t *testing.T) {
	classes := map[string]aquisicao.ClasseDeDado{
		"amostra":         aquisicao.ClasseAmostra,
		"evento discreto": aquisicao.ClasseEventoDiscreto,
	}

	// Uma politica por entrada, e nao um bloco unico de verificacoes: assim a saida
	// do teste diz QUAL politica ficou indefinida, em vez de apontar uma linha
	// perdida no meio de uma funcao longa.
	politicas := map[string]func(aquisicao.ClasseDeDado) bool{
		"garantia de entrega": func(c aquisicao.ClasseDeDado) bool {
			return c.GarantiaDeEntrega() != 0
		},
		"politica de saturacao": func(c aquisicao.ClasseDeDado) bool {
			return c.PoliticaDeSaturacao() != 0
		},
		"orcamento de latencia": func(c aquisicao.ClasseDeDado) bool {
			return c.LatenciaMaximaDeEntrega() > 0
		},
		"durabilidade local": func(c aquisicao.ClasseDeDado) bool {
			return c.DurabilidadeLocal() != 0
		},
		"retencao": func(c aquisicao.ClasseDeDado) bool {
			retencao := c.PoliticaDeRetencao()
			return retencao.Bruta > 0 && retencao.Final >= retencao.Bruta
		},
	}

	for nomeDaClasse, classe := range classes {
		t.Run(nomeDaClasse, func(t *testing.T) {
			if !classe.Valida() {
				t.Fatal("classe deveria ser valida")
			}
			if classe.String() == "unknown" {
				t.Error("classe declarada sem nome estavel")
			}
			for nomeDaPolitica, declarada := range politicas {
				if !declarada(classe) {
					t.Errorf("politica nao declarada: %s", nomeDaPolitica)
				}
			}
		})
	}
}

// TestEventoDiscretoNaoTolera perda verifica o par de politicas que define a
// classe: entrega garantida e nenhum descarte silencioso.
func TestEventoDiscretoNaoToleraPerda(t *testing.T) {
	classe := aquisicao.ClasseEventoDiscreto

	if classe.GarantiaDeEntrega() != aquisicao.EntregaAoMenosUmaVez {
		t.Error("evento discreto precisa de entrega ao-menos-uma-vez")
	}
	if classe.PoliticaDeSaturacao() != aquisicao.SaturacaoRegistrarLacuna {
		t.Error("evento discreto nao pode ser descartado em silencio")
	}
	if classe.DurabilidadeLocal() != aquisicao.DurabilidadeEmDisco {
		t.Error("evento discreto precisa sobreviver a um reinicio da origem, nao so a queda de rede")
	}
	if classe.PoliticaDeRetencao().Agregavel {
		t.Error("evento discreto e contado individualmente por relatorio; agregar destroi a resposta")
	}
}

// TestClasseInvalidaErraParaOLadoDePreservarDado documenta o comportamento de
// contorno das politicas.
//
// Uma ClasseDeDado fora da faixa nunca chega aqui — NovoCatalogoDeConteudo a
// recusa na montagem. Mas se chegasse, a resposta correta e a mais conservadora:
// preservar dado custa disco, descarta-lo custa o dado.
func TestClasseInvalidaErraParaOLadoDePreservarDado(t *testing.T) {
	invalida := aquisicao.ClasseDeDado(200)

	if invalida.Valida() {
		t.Fatal("classe fora da faixa nao deveria ser valida")
	}
	if invalida.GarantiaDeEntrega() != aquisicao.EntregaAoMenosUmaVez {
		t.Error("classe invalida deveria cair na garantia mais forte")
	}
	if invalida.PoliticaDeSaturacao() != aquisicao.SaturacaoRegistrarLacuna {
		t.Error("classe invalida deveria cair na politica que nao descarta em silencio")
	}
	if invalida.PoliticaDeRetencao().Agregavel {
		t.Error("classe invalida deveria cair na retencao que nao agrega")
	}
}
