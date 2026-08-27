package aquisicao

import (
	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

// TipoLacunaDeBuffer admite explicitamente que houve descarte.
const TipoLacunaDeBuffer TipoDeConteudo = "lacuna_de_buffer"

const operacaoDecodificarLacuna = "aquisicao.DecodificarLacunaDeBuffer"

// LacunaDeBuffer existe para que perda NUNCA seja silenciosa.
//
// Em vez de um buraco invisivel num relatorio, a lacuna vira um fato VISIVEL no
// dado, com intervalo e contagem. E a diferenca pratica entre um sistema que mente
// e um que admite o que nao sabe — e a razao de ela ser ClasseEventoDiscreto: uma
// admissao de perda que se perde no caminho e pior que inutil.
type LacunaDeBuffer struct {
	RegistrosPerdidos        uint64
	PrimeiraSequenciaPerdida uint64
	UltimaSequenciaPerdida   uint64
}

// Tipo implementa ConteudoDecodificado.
func (l LacunaDeBuffer) Tipo() TipoDeConteudo { return TipoLacunaDeBuffer }

// CamposProjetados implementa ConteudoDecodificado.
func (l LacunaDeBuffer) CamposProjetados() []CampoProjetado {
	return []CampoProjetado{
		{Nome: "lost_record_count", Valor: ValorNumerico(l.RegistrosPerdidos)},
		{Nome: "first_lost_sequence", Valor: ValorNumerico(l.PrimeiraSequenciaPerdida)},
		{Nome: "last_lost_sequence", Valor: ValorNumerico(l.UltimaSequenciaPerdida)},
	}
}

// DefinicaoDeLacunaDeBuffer devolve a definicao de catalogo deste tipo.
func DefinicaoDeLacunaDeBuffer() DefinicaoDeConteudo {
	return definirConteudo(TipoLacunaDeBuffer, ClasseEventoDiscreto,
		"Admissao explicita de descarte, com intervalo e contagem dos registros perdidos.",
		func() *contratov1.LacunaDeBuffer { return &contratov1.LacunaDeBuffer{} },
		func(doFio *contratov1.LacunaDeBuffer) (ConteudoDecodificado, error) {
			lacuna := LacunaDeBuffer{
				RegistrosPerdidos:        doFio.GetRegistrosPerdidos(),
				PrimeiraSequenciaPerdida: doFio.GetPrimeiraSequenciaPerdida(),
				UltimaSequenciaPerdida:   doFio.GetUltimaSequenciaPerdida(),
			}

			// Uma lacuna de zero registros nao e lacuna: e ruido que faria o
			// operador procurar um problema inexistente.
			if lacuna.RegistrosPerdidos == 0 {
				return nil, falha.Nova(falha.CategoriaEntradaInvalida,
					operacaoDecodificarLacuna, "lacuna de buffer sem registros perdidos")
			}
			if lacuna.UltimaSequenciaPerdida < lacuna.PrimeiraSequenciaPerdida {
				return nil, falha.Nova(falha.CategoriaEntradaInvalida,
					operacaoDecodificarLacuna, "lacuna de buffer com intervalo invertido")
			}
			return lacuna, nil
		})
}
