// Package codecdefio traduz entre o contrato de fio e o dominio.
//
// E o unico lugar do sistema que conhece as duas representacoes ao mesmo tempo.
// Acima dele so existe Envelope; abaixo, so existem bytes.
//
// A decisao de desenho que sustenta o package: o tipo de conteudo e descoberto
// por REFLEXAO sobre o descritor do protobuf, nunca por um switch sobre o oneof.
// Um switch aqui seria a segunda lista de tipos do sistema — a primeira e
// aquisicao.TodasAsDefinicoes — e duas listas do mesmo conjunto divergem. Com
// reflexao, acrescentar um tipo ao contrato e ao catalogo basta, e o codec nem
// precisa ser recompilado com conhecimento novo.
package codecdefio

import (
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const (
	operacaoDecodificarRemessa = "codecdefio.DecodificarRemessa"

	// EnvelopesMaximosPorRemessa limita o lote.
	//
	// Sem limite, uma origem comprometida derruba o gateway com uma unica
	// requisicao — e o custo nao e so memoria: cada envelope custa decodificacao,
	// validacao e uma linha de diario. O valor acompanha o tamanho de lote
	// dimensionado, com folga de uma ordem de grandeza.
	EnvelopesMaximosPorRemessa = 1000
)

// oneofDeConteudo e o descritor do `oneof` que carrega o conteudo de um envelope.
//
// Resolvido uma vez, na inicializacao do package, e nao a cada mensagem: a busca
// por nome no descritor custa mais que o resto da decodificacao, e isso roda no
// caminho quente da ingestao.
//
// A falha e panico de proposito, e e a excecao a regra de "falha como valor":
// isto nao e entrada do usuario, e o contrato compilado dentro deste binario. Se
// ele nao tem o oneof esperado, o binario esta errado, e falhar na partida e
// infinitamente melhor que aceitar dado que nao sabemos interpretar.
var oneofDeConteudo = resolverOneofDeConteudo()

func resolverOneofDeConteudo() protoreflect.OneofDescriptor {
	descritor := (&contratov1.Envelope{}).ProtoReflect().Descriptor()
	oneof := descritor.Oneofs().ByName(protoreflect.Name(aquisicao.NomeDoOneofDeConteudo))
	if oneof == nil {
		panic("codecdefio: o contrato compilado nao tem o oneof " + aquisicao.NomeDoOneofDeConteudo)
	}
	return oneof
}

// RemessaDecodificada e o resultado de interpretar uma remessa do fio.
//
// Os envelopes recusados NAO derrubam a remessa inteira: uma mensagem malformada
// entre mil validas nao pode custar as outras 999. Eles saem separados para que a
// confirmacao possa dizer a origem exatamente o que descartar — retransmitir um
// conteudo invalido nao adianta, e uma origem que tenta para sempre nunca mais
// avanca.
type RemessaDecodificada struct {
	IDDaInstalacao string

	// Envelopes sao os validos, na ordem em que chegaram.
	Envelopes []aquisicao.Envelope

	// SequenciasRejeitadas sao os numeros de sequencia recusados definitivamente.
	SequenciasRejeitadas []uint64

	// MotivosDaRejeicao acompanha SequenciasRejeitadas na mesma ordem, para log e
	// diagnostico. Separado da lista de numeros porque a confirmacao no fio leva
	// so os numeros — a origem nao tem o que fazer com o motivo, mas o operador tem.
	MotivosDaRejeicao []error
}

// DecodificarRemessa interpreta os bytes de uma remessa e constroi os envelopes
// canonicos.
//
// instanteObservado e carimbado pelo GATEWAY, nunca pela origem, e e o mesmo para
// toda a remessa: os envelopes de um lote chegaram juntos, e fingir carimbos
// distintos inventaria uma precisao que a recepcao nao tem.
//
// A identidade reivindicada na remessa NAO e conferida aqui. Quem prova identidade
// e o transporte (mTLS), e a comparacao entre o provado e o reivindicado pertence
// ao adaptador de ingresso, que e quem tem acesso a credencial. Fazer a
// conferencia aqui exigiria passar a identidade autenticada para dentro do codec,
// espalhando decisao de seguranca por uma camada que nao deveria tomar nenhuma.
func DecodificarRemessa(bruto []byte, instanteObservado time.Time,
	catalogo *aquisicao.CatalogoDeConteudo) (RemessaDecodificada, error) {

	opcoes := proto.UnmarshalOptions{DiscardUnknown: false, RecursionLimit: 100}

	var remessa contratov1.Remessa
	if err := opcoes.Unmarshal(bruto, &remessa); err != nil {
		return RemessaDecodificada{}, falha.Envolver(falha.CategoriaEntradaInvalida,
			operacaoDecodificarRemessa, "remessa malformada", err)
	}

	envelopesDoFio := remessa.GetEnvelopes()
	if len(envelopesDoFio) == 0 {
		return RemessaDecodificada{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoDecodificarRemessa, "remessa sem envelopes")
	}
	if len(envelopesDoFio) > EnvelopesMaximosPorRemessa {
		return RemessaDecodificada{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoDecodificarRemessa, "remessa excede o numero maximo de envelopes")
	}

	decodificada := RemessaDecodificada{
		IDDaInstalacao: remessa.GetIdDaInstalacao(),
		Envelopes:      make([]aquisicao.Envelope, 0, len(envelopesDoFio)),
	}

	for _, envelopeDoFio := range envelopesDoFio {
		envelope, err := decodificarEnvelope(&remessa, envelopeDoFio, instanteObservado, catalogo)
		if err != nil {
			// Falha do GATEWAY nao vira rejeicao definitiva: mandar a origem
			// descartar dado bom por causa de um defeito nosso seria perder dado
			// real para encobrir erro proprio. Ela sobe e a remessa inteira falha,
			// para que a origem retransmita quando o gateway se resolver.
			if falha.TemCategoria(err, falha.CategoriaInterna) {
				return RemessaDecodificada{}, err
			}
			decodificada.SequenciasRejeitadas = append(
				decodificada.SequenciasRejeitadas, envelopeDoFio.GetNumeroDeSequencia())
			decodificada.MotivosDaRejeicao = append(decodificada.MotivosDaRejeicao, err)
			continue
		}
		decodificada.Envelopes = append(decodificada.Envelopes, envelope)
	}

	return decodificada, nil
}

// decodificarEnvelope constroi um Envelope canonico a partir de um envelope do fio.
func decodificarEnvelope(remessa *contratov1.Remessa, envelopeDoFio *contratov1.Envelope,
	instanteObservado time.Time, catalogo *aquisicao.CatalogoDeConteudo) (aquisicao.Envelope, error) {

	tipo, conteudo, err := extrairConteudo(envelopeDoFio)
	if err != nil {
		return aquisicao.Envelope{}, err
	}

	return aquisicao.NovoEnvelope(aquisicao.ParametrosDeEnvelope{
		VersaoDoEsquema:   uint16(remessa.GetVersaoDoEsquema()),
		IDDoDispositivo:   remessa.GetIdDoDispositivo(),
		IDDaSessaoDeBoot:  remessa.GetIdDaSessaoDeBoot(),
		NumeroDeSequencia: envelopeDoFio.GetNumeroDeSequencia(),
		TempoLigadoMs:     envelopeDoFio.GetTempoLigadoMs(),
		Tipo:              tipo,
		Conteudo:          conteudo,
		InstanteObservado: instanteObservado,
	}, catalogo)
}

// extrairConteudo descobre qual conteudo o envelope carrega e devolve seus bytes.
//
// O nome do campo do oneof E o identificador do tipo de conteudo no dominio. Nao
// e convencao frouxa: o teste de cobertura do catalogo reprova o build se algum
// campo do contrato nao tiver definicao correspondente, e vice-versa.
func extrairConteudo(envelopeDoFio *contratov1.Envelope) (string, []byte, error) {
	reflexo := envelopeDoFio.ProtoReflect()

	campo := reflexo.WhichOneof(oneofDeConteudo)
	if campo == nil {
		return "", nil, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoDecodificarRemessa, "envelope sem conteudo")
	}

	mensagem := reflexo.Get(campo).Message().Interface()

	// Deterministic garante que a mesma mensagem produza sempre os mesmos bytes.
	// Importa porque estes bytes vao para o diario e sao o que permite
	// reprocessar: uma serializacao que variasse entre execucoes tornaria
	// impossivel comparar o gravado com o recebido.
	//
	// Campos DESCONHECIDOS sobrevivem a este ciclo — o protobuf os preserva. E o
	// que permite a uma origem com firmware mais novo mandar dado que este binario
	// nao entende sem que ele se perca: o proximo gateway saberá le-lo.
	bytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(mensagem)
	if err != nil {
		return "", nil, falha.Envolver(falha.CategoriaInterna,
			operacaoDecodificarRemessa, "falha ao serializar o conteudo do envelope", err)
	}

	return string(campo.Name()), bytes, nil
}
