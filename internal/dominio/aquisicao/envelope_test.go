package aquisicao_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

// instanteDeReferencia e o carimbo de recepcao usado nos testes. Fixo para que
// nenhum teste dependa do relogio da maquina que o executa.
var instanteDeReferencia = time.Date(2026, time.August, 26, 14, 30, 0, 0, time.UTC)

func catalogoDeTeste(t *testing.T) *aquisicao.CatalogoDeConteudo {
	t.Helper()
	catalogo, err := aquisicao.NovoCatalogoDeConteudo(aquisicao.TodasAsDefinicoes()...)
	if err != nil {
		t.Fatalf("montagem do catalogo de teste falhou: %v", err)
	}
	return catalogo
}

// conteudoDeAmostraEscalar serializa uma amostra escalar valida.
func conteudoDeAmostraEscalar(t *testing.T, valor float32) []byte {
	t.Helper()
	bytes, err := proto.Marshal(&contratov1.AmostraEscalar{
		Endereco: &contratov1.EnderecoDeCanal{
			IndiceDoModulo: proto.Uint32(0),
			IndiceDoCanal:  proto.Uint32(3),
		},
		Valor: proto.Float32(valor),
	})
	if err != nil {
		t.Fatalf("serializacao do conteudo de teste falhou: %v", err)
	}
	return bytes
}

func parametrosValidos(t *testing.T) aquisicao.ParametrosDeEnvelope {
	t.Helper()
	return aquisicao.ParametrosDeEnvelope{
		VersaoDoEsquema:   1,
		IDDoDispositivo:   "prensa-01",
		IDDaSessaoDeBoot:  "boot-7f3a",
		NumeroDeSequencia: 42,
		TempoLigadoMs:     90_000,
		Tipo:              string(aquisicao.TipoAmostraEscalar),
		Conteudo:          conteudoDeAmostraEscalar(t, 65.4),
		InstanteObservado: instanteDeReferencia,
	}
}

func TestNovoEnvelopeAceitaMensagemValida(t *testing.T) {
	envelope, err := aquisicao.NovoEnvelope(parametrosValidos(t), catalogoDeTeste(t))
	if err != nil {
		t.Fatalf("envelope valido deveria ser aceito: %v", err)
	}

	if envelope.TempoLigado() != 90*time.Second {
		t.Errorf("tempo ligado = %v, esperado 90s", envelope.TempoLigado())
	}
	if envelope.ClasseDeDado() != aquisicao.ClasseAmostra {
		t.Errorf("classe = %v, esperado ClasseAmostra", envelope.ClasseDeDado())
	}
	if chave := envelope.ChaveDeIdempotencia().String(); chave != "prensa-01:boot-7f3a:42" {
		t.Errorf("chave de idempotencia = %q", chave)
	}

	amostra, ehAmostra := envelope.ConteudoDecodificado().(aquisicao.AmostraEscalar)
	if !ehAmostra {
		t.Fatalf("conteudo decodificado = %T, esperado AmostraEscalar", envelope.ConteudoDecodificado())
	}
	if amostra.Valor != 65.4 {
		t.Errorf("valor = %v, esperado 65.4", amostra.Valor)
	}
	if amostra.Endereco.IndiceDoCanal != 3 {
		t.Errorf("indice do canal = %d, esperado 3", amostra.Endereco.IndiceDoCanal)
	}
}

// TestNovoEnvelopeRecusaTempoLigadoQueEstouraAConversao cobre um defeito real,
// encontrado por teste antes de existir codigo em campo.
//
// Um tempo ligado hostil como 1<<62 milissegundos, convertido para time.Duration
// ANTES da comparacao, estoura o int64 e envolve para um valor pequeno e
// plausivel: a validacao passaria e o quadro corrompido entraria no sistema.
//
// A regra que decorre, e que este teste protege: VALIDAR NA LARGURA EM QUE O DADO
// CHEGOU, antes de qualquer conversao. Quem escolhe o numero e o adversario.
func TestNovoEnvelopeRecusaTempoLigadoQueEstouraAConversao(t *testing.T) {
	hostis := map[string]uint64{
		"potencia de dois que envolve o int64": 1 << 62,
		"maximo do uint64":                     ^uint64(0),
		"acima de cem anos":                    uint64(101*365*24*time.Hour/time.Millisecond) + 1,
	}

	for nome, tempoLigado := range hostis {
		t.Run(nome, func(t *testing.T) {
			parametros := parametrosValidos(t)
			parametros.TempoLigadoMs = tempoLigado

			envelope, err := aquisicao.NovoEnvelope(parametros, catalogoDeTeste(t))
			if err == nil {
				t.Fatalf("tempo ligado %d deveria ser recusado, mas produziu envelope com %v",
					tempoLigado, envelope.TempoLigado())
			}
			if !falha.TemCategoria(err, falha.CategoriaEntradaInvalida) {
				t.Errorf("categoria = %v, esperado CategoriaEntradaInvalida", falha.CategoriaDe(err))
			}
		})
	}
}

func TestNovoEnvelopeRecusaEntradaInvalida(t *testing.T) {
	casos := map[string]func(*aquisicao.ParametrosDeEnvelope){
		"versao de esquema abaixo da faixa": func(p *aquisicao.ParametrosDeEnvelope) {
			p.VersaoDoEsquema = 0
		},
		"versao de esquema acima da faixa": func(p *aquisicao.ParametrosDeEnvelope) {
			p.VersaoDoEsquema = aquisicao.VersaoMaximaDoEsquema + 1
		},
		"dispositivo vazio": func(p *aquisicao.ParametrosDeEnvelope) {
			p.IDDoDispositivo = ""
		},
		"dispositivo fora do alfabeto": func(p *aquisicao.ParametrosDeEnvelope) {
			p.IDDoDispositivo = "Prensa_01"
		},
		"sessao de boot vazia": func(p *aquisicao.ParametrosDeEnvelope) {
			p.IDDaSessaoDeBoot = ""
		},
		"conteudo vazio": func(p *aquisicao.ParametrosDeEnvelope) {
			p.Conteudo = nil
		},
		"conteudo acima do limite": func(p *aquisicao.ParametrosDeEnvelope) {
			p.Conteudo = make([]byte, aquisicao.TamanhoMaximoDoConteudoEmBytes+1)
		},
		"tipo desconhecido": func(p *aquisicao.ParametrosDeEnvelope) {
			p.Tipo = "conteudo.inexistente"
		},
		"conteudo que nao decodifica para o tipo": func(p *aquisicao.ParametrosDeEnvelope) {
			p.Conteudo = []byte{0xff, 0xff, 0xff, 0xff}
		},
	}

	for nome, corromper := range casos {
		t.Run(nome, func(t *testing.T) {
			parametros := parametrosValidos(t)
			corromper(&parametros)

			if _, err := aquisicao.NovoEnvelope(parametros, catalogoDeTeste(t)); err == nil {
				t.Fatal("entrada invalida deveria ser recusada")
			} else if !falha.TemCategoria(err, falha.CategoriaEntradaInvalida) {
				t.Errorf("categoria = %v, esperado CategoriaEntradaInvalida", falha.CategoriaDe(err))
			}
		})
	}
}

// TestNovoEnvelopeCulpaOGatewayPorFalhaDoGateway verifica que a taxonomia nao
// culpa a origem por erro nosso.
//
// A distincao nao e cosmetica: CategoriaEntradaInvalida vira 400 e faz a origem
// DESCARTAR a mensagem, porque retransmitir nao adiantaria. Se um esquecimento do
// gateway fosse classificado assim, ele mandaria a planta jogar dado bom fora.
func TestNovoEnvelopeCulpaOGatewayPorFalhaDoGateway(t *testing.T) {
	t.Run("instante de observacao nao carimbado", func(t *testing.T) {
		parametros := parametrosValidos(t)
		parametros.InstanteObservado = time.Time{}

		_, err := aquisicao.NovoEnvelope(parametros, catalogoDeTeste(t))
		if !falha.TemCategoria(err, falha.CategoriaInterna) {
			t.Errorf("categoria = %v, esperado CategoriaInterna", falha.CategoriaDe(err))
		}
	})

	t.Run("catalogo nao fornecido", func(t *testing.T) {
		_, err := aquisicao.NovoEnvelope(parametrosValidos(t), nil)
		if !falha.TemCategoria(err, falha.CategoriaInterna) {
			t.Errorf("categoria = %v, esperado CategoriaInterna", falha.CategoriaDe(err))
		}
	})
}

// TestEnvelopeNaoCompartilhaOBufferDoAdaptador protege a copia defensiva.
//
// O buffer de origem pertence ao adaptador de transporte e costuma ser
// reaproveitado entre mensagens. Sem a copia, o Envelope mudaria de conteudo
// depois de construido — e o que fosse gravado no diario nao seria o que foi
// validado.
func TestEnvelopeNaoCompartilhaOBufferDoAdaptador(t *testing.T) {
	parametros := parametrosValidos(t)
	bufferDoAdaptador := parametros.Conteudo

	envelope, err := aquisicao.NovoEnvelope(parametros, catalogoDeTeste(t))
	if err != nil {
		t.Fatalf("envelope valido deveria ser aceito: %v", err)
	}
	antes := envelope.ConteudoBruto()

	// O adaptador reaproveita o buffer para a proxima mensagem.
	for indice := range bufferDoAdaptador {
		bufferDoAdaptador[indice] = 0
	}

	depois := envelope.ConteudoBruto()
	if string(antes) != string(depois) {
		t.Error("o envelope mudou de conteudo quando o adaptador reaproveitou o buffer")
	}

	// E o inverso: quem recebe ConteudoBruto nao pode corromper o envelope.
	copia := envelope.ConteudoBruto()
	for indice := range copia {
		copia[indice] = 0
	}
	if string(envelope.ConteudoBruto()) != string(antes) {
		t.Error("mutar o resultado de ConteudoBruto alterou o envelope")
	}
}
