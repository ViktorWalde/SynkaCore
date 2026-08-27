package diariosqlite_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
)

// parametrosParaMedicao devolve os argumentos de um envelope tipico.
//
// Amostra escalar porque e o conteudo mais frequente do sistema por larga margem:
// medir com o caso raro produziria um numero que nao descreve a operacao real.
func parametrosParaMedicao(b *testing.B) aquisicao.ParametrosDeEnvelope {
	b.Helper()

	conteudo, err := proto.Marshal(&contratov1.AmostraEscalar{
		Endereco: &contratov1.EnderecoDeCanal{IndiceDoCanal: proto.Uint32(1)},
		Valor:    proto.Float32(24.5),
	})
	if err != nil {
		b.Fatalf("serializacao falhou: %v", err)
	}

	return aquisicao.ParametrosDeEnvelope{
		VersaoDoEsquema:   1,
		IDDoDispositivo:   "prensa-01",
		IDDaSessaoDeBoot:  "boot-medicao",
		NumeroDeSequencia: 1,
		TempoLigadoMs:     1_000,
		Tipo:              string(aquisicao.TipoAmostraEscalar),
		Conteudo:          conteudo,
		InstanteObservado: time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC),
	}
}

// envelopeParaMedicao constroi um envelope valido com a sequencia indicada.
func envelopeParaMedicao(b *testing.B, sessao string, sequencia uint64) aquisicao.Envelope {
	b.Helper()

	catalogo, err := aquisicao.NovoCatalogoDeConteudo(aquisicao.TodasAsDefinicoes()...)
	if err != nil {
		b.Fatalf("catalogo falhou: %v", err)
	}

	parametros := parametrosParaMedicao(b)
	parametros.IDDaSessaoDeBoot = sessao
	parametros.NumeroDeSequencia = sequencia

	envelope, err := aquisicao.NovoEnvelope(parametros, catalogo)
	if err != nil {
		b.Fatalf("envelope invalido: %v", err)
	}
	return envelope
}
