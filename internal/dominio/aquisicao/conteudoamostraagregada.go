package aquisicao

import (
	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

// TipoAmostraAgregada resume muitas amostras colhidas rapido demais para trafegar.
const TipoAmostraAgregada TipoDeConteudo = "amostra_agregada"

const operacaoDecodificarAmostraAgregada = "aquisicao.DecodificarAmostraAgregada"

// AmostraAgregada carrega indicadores calculados pela origem sobre uma janela.
//
// Razao de existir: vibracao e corrente exigem centenas a milhares de amostras por
// segundo, e esse sinal nao pode trafegar bruto. A origem calcula os indicadores
// e envia o resumo.
//
// Isso e CONDICIONAMENTO DE SINAL, nao regra de negocio — o equivalente digital do
// que um transdutor analogico ja faria em hardware. A origem nao decide o que o
// numero significa; decidir que "valor eficaz acima de 5 e alarme" continua sendo
// exclusividade do gateway. A regra do projeto nao e "a origem nao calcula", e
// "A ORIGEM NAO DECIDE".
type AmostraAgregada struct {
	Endereco EnderecoDeCanal

	// JanelaMs e a duracao da janela agregada. O tempo ligado do Envelope marca o
	// FIM da janela.
	JanelaMs uint32

	// QuantidadeDeAmostras e quantas amostras brutas entraram.
	//
	// Obrigatoria, e nao opcional por conveniencia: sem ela, reagregar janelas no
	// gateway produziria media de medias sem peso, que e simplesmente errada. E
	// sem os campos de janela e contagem, um valor eficaz de 1024 amostras
	// chegaria indistinguivel de uma leitura unica, e toda estatistica posterior
	// estaria errada sem aviso.
	QuantidadeDeAmostras uint32

	Minimo float32
	Maximo float32
	Media  float32

	// ValorEficaz e o indicador que importa em vibracao e corrente alternada, onde
	// a media tende a zero e nao diz nada.
	ValorEficaz float32
}

// Tipo implementa ConteudoDecodificado.
func (a AmostraAgregada) Tipo() TipoDeConteudo { return TipoAmostraAgregada }

// CamposProjetados implementa ConteudoDecodificado.
func (a AmostraAgregada) CamposProjetados() []CampoProjetado {
	return append(camposDoEndereco(a.Endereco),
		CampoProjetado{Nome: "window_ms", Valor: ValorNumerico(a.JanelaMs)},
		CampoProjetado{Nome: "sample_count", Valor: ValorNumerico(a.QuantidadeDeAmostras)},
		CampoProjetado{Nome: "minimum", Valor: ValorNumerico(a.Minimo)},
		CampoProjetado{Nome: "maximum", Valor: ValorNumerico(a.Maximo)},
		CampoProjetado{Nome: "mean", Valor: ValorNumerico(a.Media)},
		CampoProjetado{Nome: "rms", Valor: ValorNumerico(a.ValorEficaz)},
	)
}

// DefinicaoDeAmostraAgregada devolve a definicao de catalogo deste tipo.
func DefinicaoDeAmostraAgregada() DefinicaoDeConteudo {
	return definirConteudo(TipoAmostraAgregada, ClasseAmostra,
		"Resumo estatistico de uma janela de amostras rapidas demais para trafegar brutas.",
		func() *contratov1.AmostraAgregada { return &contratov1.AmostraAgregada{} },
		func(doFio *contratov1.AmostraAgregada) (ConteudoDecodificado, error) {
			agregada := AmostraAgregada{
				Endereco:             enderecoDe(doFio.GetEndereco()),
				JanelaMs:             doFio.GetJanelaMs(),
				QuantidadeDeAmostras: doFio.GetQuantidadeDeAmostras(),
				Minimo:               doFio.GetMinimo(),
				Maximo:               doFio.GetMaximo(),
				Media:                doFio.GetMedia(),
				ValorEficaz:          doFio.GetValorEficaz(),
			}

			// Uma agregacao sem contagem nao e reagregavel, e uma janela de duracao
			// zero nao descreve intervalo nenhum. Recusar na entrada e mais barato
			// que descobrir meses depois que a serie consolidada esta errada e nao
			// da para recomputar.
			if agregada.QuantidadeDeAmostras == 0 {
				return nil, falha.Nova(falha.CategoriaEntradaInvalida,
					operacaoDecodificarAmostraAgregada,
					"amostra agregada sem quantidade de amostras: resumo nao reagregavel")
			}
			if agregada.JanelaMs == 0 {
				return nil, falha.Nova(falha.CategoriaEntradaInvalida,
					operacaoDecodificarAmostraAgregada,
					"amostra agregada com janela de duracao zero")
			}
			if agregada.Minimo > agregada.Maximo {
				return nil, falha.Nova(falha.CategoriaEntradaInvalida,
					operacaoDecodificarAmostraAgregada,
					"amostra agregada com minimo maior que o maximo: leitura incoerente")
			}
			return agregada, nil
		})
}
