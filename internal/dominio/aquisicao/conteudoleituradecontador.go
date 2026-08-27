package aquisicao

import (
	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
)

// TipoLeituraDeContador e a contagem acumulada de um canal na sessao de boot.
const TipoLeituraDeContador TipoDeConteudo = "leitura_de_contador"

// LeituraDeContador carrega uma contagem ACUMULADA, nunca incremental.
//
// Um incremento perdido some para sempre e a contagem fica permanentemente
// errada. Uma leitura acumulada perdida e reposta pela proxima: a perda se cura
// sozinha. Como contagem de producao e numero que o gestor usa para decidir, essa
// diferenca separa um relatorio confiavel de um plausivel.
//
// Inteiro, e nao ponto flutuante: acumulador em float acumula erro de
// arredondamento indefinidamente, e o erro cresce justamente quando o numero fica
// grande — que e quando alguem vai olhar. Energia em Wh, volume em mL; a
// conversao para unidade de exibicao e do gateway.
type LeituraDeContador struct {
	Endereco          EnderecoDeCanal
	ContagemAcumulada uint64
}

// Tipo implementa ConteudoDecodificado.
func (l LeituraDeContador) Tipo() TipoDeConteudo { return TipoLeituraDeContador }

// CamposProjetados implementa ConteudoDecodificado.
func (l LeituraDeContador) CamposProjetados() []CampoProjetado {
	return append(camposDoEndereco(l.Endereco),
		CampoProjetado{Nome: "accumulated_count", Valor: ValorNumerico(l.ContagemAcumulada)},
	)
}

// DefinicaoDeLeituraDeContador devolve a definicao de catalogo deste tipo.
func DefinicaoDeLeituraDeContador() DefinicaoDeConteudo {
	return definirConteudo(TipoLeituraDeContador, ClasseEventoDiscreto,
		"Contagem acumulada de um canal dentro da sessao de boot.",
		func() *contratov1.LeituraDeContador { return &contratov1.LeituraDeContador{} },
		func(doFio *contratov1.LeituraDeContador) (ConteudoDecodificado, error) {
			return LeituraDeContador{
				Endereco:          enderecoDe(doFio.GetEndereco()),
				ContagemAcumulada: doFio.GetContagemAcumulada(),
			}, nil
		})
}
