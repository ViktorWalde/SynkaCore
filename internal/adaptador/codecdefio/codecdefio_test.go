package codecdefio_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/codecdefio"
	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

var instanteDeReferencia = time.Date(2026, time.August, 26, 14, 30, 0, 0, time.UTC)

func catalogoDeTeste(t *testing.T) *aquisicao.CatalogoDeConteudo {
	t.Helper()
	catalogo, err := aquisicao.NovoCatalogoDeConteudo(aquisicao.TodasAsDefinicoes()...)
	if err != nil {
		t.Fatalf("montagem do catalogo falhou: %v", err)
	}
	return catalogo
}

func serializar(t *testing.T, remessa *contratov1.Remessa) []byte {
	t.Helper()
	bytes, err := proto.Marshal(remessa)
	if err != nil {
		t.Fatalf("serializacao da remessa falhou: %v", err)
	}
	return bytes
}

func remessaValida(envelopes ...*contratov1.Envelope) *contratov1.Remessa {
	return &contratov1.Remessa{
		VersaoDoEsquema:  proto.Uint32(1),
		IdDaInstalacao:   proto.String("planta-piloto"),
		IdDoDispositivo:  proto.String("prensa-01"),
		IdDaSessaoDeBoot: proto.String("boot-7f3a"),
		Envelopes:        envelopes,
	}
}

func envelopeDeAmostra(sequencia uint64, valor float32) *contratov1.Envelope {
	return &contratov1.Envelope{
		NumeroDeSequencia: proto.Uint64(sequencia),
		TempoLigadoMs:     proto.Uint64(sequencia * 1000),
		Conteudo: &contratov1.Envelope_AmostraEscalar{
			AmostraEscalar: &contratov1.AmostraEscalar{
				Endereco: &contratov1.EnderecoDeCanal{IndiceDoCanal: proto.Uint32(2)},
				Valor:    proto.Float32(valor),
			},
		},
	}
}

// TestTipoDeConteudoVemDoDescritorEmVezDeUmSwitch e a verificacao que sustenta a
// decisao central do codec.
//
// O tipo e descoberto por REFLEXAO sobre o descritor do protobuf, e o nome do
// campo do oneof E o identificador do tipo no dominio. Um switch aqui seria a
// segunda lista de tipos do sistema — a primeira e TodasAsDefinicoes — e duas
// listas do mesmo conjunto divergem.
func TestTipoDeConteudoVemDoDescritorEmVezDeUmSwitch(t *testing.T) {
	casos := map[string]struct {
		envelope *contratov1.Envelope
		esperado aquisicao.TipoDeConteudo
	}{
		"amostra escalar": {
			envelope: envelopeDeAmostra(1, 65.4),
			esperado: aquisicao.TipoAmostraEscalar,
		},
		"leitura de contador": {
			envelope: &contratov1.Envelope{
				NumeroDeSequencia: proto.Uint64(1),
				Conteudo: &contratov1.Envelope_LeituraDeContador{
					LeituraDeContador: &contratov1.LeituraDeContador{
						ContagemAcumulada: proto.Uint64(42),
					}}},
			esperado: aquisicao.TipoLeituraDeContador,
		},
		"mudanca de estado de maquina": {
			envelope: &contratov1.Envelope{
				NumeroDeSequencia: proto.Uint64(1),
				Conteudo: &contratov1.Envelope_MudancaDeEstadoDeMaquina{
					MudancaDeEstadoDeMaquina: &contratov1.MudancaDeEstadoDeMaquina{
						Estado: contratov1.EstadoDeMaquina_ESTADO_DE_MAQUINA_QUEBRA.Enum(),
					}}},
			esperado: aquisicao.TipoMudancaDeEstadoDeMaquina,
		},
	}

	for nome, caso := range casos {
		t.Run(nome, func(t *testing.T) {
			decodificada, err := codecdefio.DecodificarRemessa(
				serializar(t, remessaValida(caso.envelope)), instanteDeReferencia, catalogoDeTeste(t))
			if err != nil {
				t.Fatalf("remessa valida deveria ser aceita: %v", err)
			}
			if len(decodificada.Envelopes) != 1 {
				t.Fatalf("envelopes decodificados = %d, esperado 1", len(decodificada.Envelopes))
			}
			if tipo := decodificada.Envelopes[0].Tipo(); tipo != caso.esperado {
				t.Errorf("tipo = %q, esperado %q", tipo, caso.esperado)
			}
		})
	}
}

func TestDecodificaRemessaCompletaComOsCamposDaOrigem(t *testing.T) {
	decodificada, err := codecdefio.DecodificarRemessa(
		serializar(t, remessaValida(envelopeDeAmostra(7, 65.4))),
		instanteDeReferencia, catalogoDeTeste(t))
	if err != nil {
		t.Fatalf("remessa valida deveria ser aceita: %v", err)
	}

	if decodificada.IDDaInstalacao != "planta-piloto" {
		t.Errorf("instalacao = %q", decodificada.IDDaInstalacao)
	}

	envelope := decodificada.Envelopes[0]
	if envelope.IDDoDispositivo().String() != "prensa-01" {
		t.Errorf("dispositivo = %q", envelope.IDDoDispositivo())
	}
	if envelope.ChaveDeIdempotencia().NumeroDeSequencia() != 7 {
		t.Errorf("sequencia = %d", envelope.ChaveDeIdempotencia().NumeroDeSequencia())
	}
	if envelope.TempoLigado() != 7*time.Second {
		t.Errorf("tempo ligado = %v", envelope.TempoLigado())
	}

	// O carimbo de recepcao e do GATEWAY, e vale para toda a remessa: os envelopes
	// de um lote chegaram juntos, e fingir carimbos distintos inventaria uma
	// precisao que a recepcao nao tem.
	if !envelope.InstanteObservado().Equal(instanteDeReferencia) {
		t.Errorf("instante observado = %v, esperado %v",
			envelope.InstanteObservado(), instanteDeReferencia)
	}
}

// TestEnvelopeInvalidoNaoDerrubaARemessaInteira e a decisao que protege o lote.
//
// Uma mensagem malformada entre mil validas nao pode custar as outras 999. A
// invalida sai separada, para que a confirmacao diga a origem exatamente o que
// descartar — retransmitir conteudo invalido nao adianta, e uma origem que tenta
// para sempre nunca mais avanca.
func TestEnvelopeInvalidoNaoDerrubaARemessaInteira(t *testing.T) {
	// O envelope do meio tem estado NAO_ESPECIFICADO, que e recusado: aceita-lo
	// seria registrar "a maquina esta em algum estado" como se fosse um fato.
	invalido := &contratov1.Envelope{
		NumeroDeSequencia: proto.Uint64(2),
		Conteudo: &contratov1.Envelope_MudancaDeEstadoDeMaquina{
			MudancaDeEstadoDeMaquina: &contratov1.MudancaDeEstadoDeMaquina{}},
	}

	decodificada, err := codecdefio.DecodificarRemessa(
		serializar(t, remessaValida(envelopeDeAmostra(1, 20), invalido, envelopeDeAmostra(3, 30))),
		instanteDeReferencia, catalogoDeTeste(t))
	if err != nil {
		t.Fatalf("a remessa nao deveria falhar inteira: %v", err)
	}

	if len(decodificada.Envelopes) != 2 {
		t.Errorf("envelopes aceitos = %d, esperado 2", len(decodificada.Envelopes))
	}
	if len(decodificada.SequenciasRejeitadas) != 1 || decodificada.SequenciasRejeitadas[0] != 2 {
		t.Errorf("sequencias rejeitadas = %v, esperado [2]", decodificada.SequenciasRejeitadas)
	}
	if len(decodificada.MotivosDaRejeicao) != len(decodificada.SequenciasRejeitadas) {
		t.Error("cada rejeicao precisa carregar seu motivo, para log e diagnostico")
	}
}

// TestRemessaEstruturalmenteInvalidaERecusadaInteira cobre o outro lado: quando o
// problema e da REMESSA, e nao de um envelope dentro dela.
func TestRemessaEstruturalmenteInvalidaERecusadaInteira(t *testing.T) {
	casos := map[string][]byte{
		"bytes que nao sao protobuf": {0xff, 0xff, 0xff, 0xff, 0xff},
		"remessa sem envelopes":      serializar(t, remessaValida()),
	}

	for nome, bruto := range casos {
		t.Run(nome, func(t *testing.T) {
			_, err := codecdefio.DecodificarRemessa(bruto, instanteDeReferencia, catalogoDeTeste(t))
			if err == nil {
				t.Fatal("remessa invalida deveria ser recusada")
			}
			if !falha.TemCategoria(err, falha.CategoriaEntradaInvalida) {
				t.Errorf("categoria = %v, esperado CategoriaEntradaInvalida", falha.CategoriaDe(err))
			}
		})
	}
}

// TestLoteExcessivoERecusado fecha o vetor de exaustao por remessa gigante.
//
// Cada envelope custa decodificacao, validacao e uma linha de diario. Sem limite,
// uma origem comprometida derruba o gateway com uma unica requisicao.
func TestLoteExcessivoERecusado(t *testing.T) {
	envelopes := make([]*contratov1.Envelope, codecdefio.EnvelopesMaximosPorRemessa+1)
	for indice := range envelopes {
		envelopes[indice] = envelopeDeAmostra(uint64(indice+1), 20)
	}

	_, err := codecdefio.DecodificarRemessa(
		serializar(t, remessaValida(envelopes...)), instanteDeReferencia, catalogoDeTeste(t))
	if err == nil {
		t.Fatal("remessa acima do limite deveria ser recusada")
	}
	if !falha.TemCategoria(err, falha.CategoriaEntradaInvalida) {
		t.Errorf("categoria = %v, esperado CategoriaEntradaInvalida", falha.CategoriaDe(err))
	}
}

// TestEnvelopeSemConteudoERejeitado cobre o oneof vazio.
func TestEnvelopeSemConteudoERejeitado(t *testing.T) {
	vazio := &contratov1.Envelope{NumeroDeSequencia: proto.Uint64(1)}

	decodificada, err := codecdefio.DecodificarRemessa(
		serializar(t, remessaValida(vazio)), instanteDeReferencia, catalogoDeTeste(t))
	if err != nil {
		t.Fatalf("a remessa nao deveria falhar inteira: %v", err)
	}
	if len(decodificada.SequenciasRejeitadas) != 1 {
		t.Errorf("sequencias rejeitadas = %v, esperado [1]", decodificada.SequenciasRejeitadas)
	}
}

// TestCampoDesconhecidoSobreviveAoCicloDeCodificacao e a propriedade que permite ao
// gateway aceitar TODAS as versoes ja publicadas do contrato.
//
// Uma origem com firmware mais novo manda campos que este binario nao conhece. Se
// eles fossem descartados na decodificacao, o dado se perderia — e o proximo
// gateway, que saberia le-los, nao teria mais o que ler. Preservados, eles voltam
// intactos na reserializacao que vai para o diario.
func TestCampoDesconhecidoSobreviveAoCicloDeCodificacao(t *testing.T) {
	// Um campo com numero alto, que nenhum tipo do contrato declara — e exatamente
	// o que uma versao futura acrescentaria.
	const numeroDeCampoFuturo = 999
	conteudoComCampoFuturo := &contratov1.AmostraEscalar{Valor: proto.Float32(65.4)}
	conteudoComCampoFuturo.ProtoReflect().SetUnknown(
		protoreflectRawVarint(numeroDeCampoFuturo, 12345))

	envelope := &contratov1.Envelope{
		NumeroDeSequencia: proto.Uint64(1),
		TempoLigadoMs:     proto.Uint64(1000),
		Conteudo: &contratov1.Envelope_AmostraEscalar{
			AmostraEscalar: conteudoComCampoFuturo,
		},
	}

	decodificada, err := codecdefio.DecodificarRemessa(
		serializar(t, remessaValida(envelope)), instanteDeReferencia, catalogoDeTeste(t))
	if err != nil {
		t.Fatalf("envelope com campo desconhecido deveria ser aceito: %v", err)
	}
	if len(decodificada.Envelopes) != 1 {
		t.Fatalf("envelopes aceitos = %d, esperado 1", len(decodificada.Envelopes))
	}

	// Os bytes canonicos que vao para o diario precisam conter o campo futuro.
	var reconstruido contratov1.AmostraEscalar
	if err := proto.Unmarshal(decodificada.Envelopes[0].ConteudoBruto(), &reconstruido); err != nil {
		t.Fatalf("conteudo bruto nao decodifica: %v", err)
	}
	if len(reconstruido.ProtoReflect().GetUnknown()) == 0 {
		t.Error("o campo desconhecido se perdeu: uma origem com firmware mais novo teria dado descartado")
	}
	if reconstruido.GetValor() != 65.4 {
		t.Errorf("valor conhecido = %v, esperado 65.4", reconstruido.GetValor())
	}
}
