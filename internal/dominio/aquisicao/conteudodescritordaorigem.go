package aquisicao

import (
	"strconv"
	"strings"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

// TipoDescritorDaOrigem declara o que a origem e e o que cada canal mede.
const TipoDescritorDaOrigem TipoDeConteudo = "descritor_da_origem"

const (
	operacaoDecodificarDescritor = "aquisicao.DecodificarDescritorDaOrigem"

	// canaisMaximosPorOrigem limita a declaracao para fechar o vetor de exaustao
	// de memoria por descritor gigante. Duzentos e cinquenta e seis canais e mais
	// do que qualquer origem realista tem, e a origem que precisar de mais esta
	// mal dividida.
	canaisMaximosPorOrigem = 256

	tamanhoMaximoDeTextoDescritivo = 64
)

// DescritorDaOrigem e a rede de protecao de comissionamento do sistema.
//
// Enviado no boot e periodicamente, em baixa frequencia de proposito: assim a
// descricao do canal NAO precisa viajar em cada amostra, o que economiza a maior
// parte dos bytes do sistema.
//
// Serve para o gateway comparar o que a origem ACREDITA medir com o mapeamento
// que ele mesmo tem — canal trocado no painel deixa de ser erro silencioso e vira
// divergencia denunciada. Toda configuracao replicada na origem declara sua
// versao, e o gateway confere; configuracao que existe nos dois lados sem
// verificacao diverge, e e questao de tempo.
type DescritorDaOrigem struct {
	VersaoDoFirmware string
	ModeloDoHardware string
	Canais           []DescritorDeCanal

	// VersaoDoCatalogoDeMotivos permite detectar deriva de vocabulario: se a
	// origem exibe rotulos de uma versao antiga, os codigos que ela envia podem
	// significar outra coisa, e o dado fica errado de forma indetectavel.
	VersaoDoCatalogoDeMotivos uint32
}

// DescritorDeCanal e o que a origem acredita que um canal seu mede.
//
// Autoritativo e o mapeamento do gateway; estes valores existem para detectar
// DISCORDANCIA entre os dois, nunca para substituir a configuracao da instalacao.
type DescritorDeCanal struct {
	Endereco EnderecoDeCanal

	// Grandeza e o que a origem acredita medir, como codigo do contrato.
	Grandeza uint32

	// Unidade em notacao UCUM (por exemplo "Cel", "kPa", "L/min", "kg").
	Unidade string

	// PeriodoDeAmostragemMs permite ao gateway saber o que esperar, e portanto
	// alarmar quando um canal fica MUDO — que e uma falha invisivel de outro modo,
	// porque a ausencia de dado nao gera evento nenhum.
	PeriodoDeAmostragemMs uint32
}

// Tipo implementa ConteudoDecodificado.
func (d DescritorDaOrigem) Tipo() TipoDeConteudo { return TipoDescritorDaOrigem }

// CamposProjetados implementa ConteudoDecodificado.
//
// Os canais sao resumidos em contagem e assinatura, e nao expandidos em uma linha
// por canal: o modelo de leitura e uma serie temporal de fatos, e um descritor e
// um fato so. A assinatura permite detectar mudanca de configuracao entre dois
// descritores sem guardar a lista inteira duas vezes.
func (d DescritorDaOrigem) CamposProjetados() []CampoProjetado {
	return []CampoProjetado{
		{Nome: "firmware_version", Valor: ValorTexto(d.VersaoDoFirmware)},
		{Nome: "hardware_model", Valor: ValorTexto(d.ModeloDoHardware)},
		{Nome: "channel_count", Valor: ValorNumerico(len(d.Canais))},
		{Nome: "channel_signature", Valor: ValorTexto(d.AssinaturaDosCanais())},
		{Nome: "reason_catalog_version", Valor: ValorNumerico(d.VersaoDoCatalogoDeMotivos)},
	}
}

// AssinaturaDosCanais devolve a forma textual canonica da configuracao de canais.
//
// Existe como funcao unica para que a comparacao entre o que a origem declara e o
// que o gateway espera use SEMPRE a mesma representacao. Duas montagens
// diferentes da mesma configuracao produziriam divergencia falsa, e um alarme de
// comissionamento que dispara sem motivo e desligado em uma semana — e ai a rede
// de protecao deixa de existir justamente quando importa.
func (d DescritorDaOrigem) AssinaturaDosCanais() string {
	var construtor strings.Builder
	for indice, canal := range d.Canais {
		if indice > 0 {
			construtor.WriteByte(';')
		}
		construtor.WriteString(canal.Endereco.String())
		construtor.WriteByte('=')
		construtor.WriteString(strconv.FormatUint(uint64(canal.Grandeza), 10))
		construtor.WriteByte('/')
		construtor.WriteString(canal.Unidade)
	}
	return construtor.String()
}

// DefinicaoDeDescritorDaOrigem devolve a definicao de catalogo deste tipo.
func DefinicaoDeDescritorDaOrigem() DefinicaoDeConteudo {
	return definirConteudo(TipoDescritorDaOrigem, ClasseAmostra,
		"Autodeclaracao da origem: firmware, hardware, canais e versao do catalogo de motivos.",
		func() *contratov1.DescritorDaOrigem { return &contratov1.DescritorDaOrigem{} },
		func(doFio *contratov1.DescritorDaOrigem) (ConteudoDecodificado, error) {
			canaisDoFio := doFio.GetCanais()
			if len(canaisDoFio) > canaisMaximosPorOrigem {
				return nil, falha.Nova(falha.CategoriaEntradaInvalida,
					operacaoDecodificarDescritor, "descritor declara canais em excesso")
			}
			descritor := DescritorDaOrigem{
				VersaoDoFirmware:          doFio.GetVersaoDoFirmware(),
				ModeloDoHardware:          doFio.GetModeloDoHardware(),
				VersaoDoCatalogoDeMotivos: doFio.GetVersaoDoCatalogoDeMotivos(),
				Canais:                    make([]DescritorDeCanal, 0, len(canaisDoFio)),
			}
			if len(descritor.VersaoDoFirmware) > tamanhoMaximoDeTextoDescritivo ||
				len(descritor.ModeloDoHardware) > tamanhoMaximoDeTextoDescritivo {
				return nil, falha.Nova(falha.CategoriaEntradaInvalida,
					operacaoDecodificarDescritor, "texto descritivo excede o comprimento maximo")
			}

			enderecosVistos := make(map[EnderecoDeCanal]struct{}, len(canaisDoFio))
			for _, canalDoFio := range canaisDoFio {
				endereco := enderecoDe(canalDoFio.GetEndereco())

				// Endereco repetido significa que o gateway nao consegue decidir o
				// que aquele canal mede. Recusar na entrada e melhor que resolver
				// pela ultima ocorrencia, que faria a interpretacao depender da
				// ordem de serializacao.
				if _, repetido := enderecosVistos[endereco]; repetido {
					return nil, falha.Nova(falha.CategoriaEntradaInvalida,
						operacaoDecodificarDescritor,
						"descritor declara o mesmo endereco de canal mais de uma vez: "+endereco.String())
				}
				enderecosVistos[endereco] = struct{}{}

				unidade := canalDoFio.GetUnidade()
				if len(unidade) > tamanhoMaximoDeTextoDescritivo {
					return nil, falha.Nova(falha.CategoriaEntradaInvalida,
						operacaoDecodificarDescritor, "unidade excede o comprimento maximo")
				}
				descritor.Canais = append(descritor.Canais, DescritorDeCanal{
					Endereco:              endereco,
					Grandeza:              uint32(canalDoFio.GetGrandeza()),
					Unidade:               unidade,
					PeriodoDeAmostragemMs: canalDoFio.GetPeriodoDeAmostragemMs(),
				})
			}
			return descritor, nil
		})
}
