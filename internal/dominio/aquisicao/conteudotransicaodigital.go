package aquisicao

import (
	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
)

// TipoTransicaoDigital registra a mudanca de estado de uma entrada discreta.
const TipoTransicaoDigital TipoDeConteudo = "transicao_digital"

// TransicaoDigital e a mudanca de estado de uma entrada discreta.
//
// E evento, nao amostra: o que importa e a TRANSICAO. Amostrar periodicamente uma
// entrada digital perderia o pulso curto, que costuma ser exatamente o fato que o
// sistema existe para contar.
type TransicaoDigital struct {
	Endereco EnderecoDeCanal
	Ativo    bool
}

// EnderecoDoCanal implementa ConteudoEnderecado.
func (t TransicaoDigital) EnderecoDoCanal() EnderecoDeCanal { return t.Endereco }

// Tipo implementa ConteudoDecodificado.
func (t TransicaoDigital) Tipo() TipoDeConteudo { return TipoTransicaoDigital }

// CamposProjetados implementa ConteudoDecodificado.
func (t TransicaoDigital) CamposProjetados() []CampoProjetado {
	return append(camposDoEndereco(t.Endereco),
		CampoProjetado{Nome: "active", Valor: ValorLogico(t.Ativo)},
	)
}

// DefinicaoDeTransicaoDigital devolve a definicao de catalogo deste tipo.
func DefinicaoDeTransicaoDigital() DefinicaoDeConteudo {
	return definirConteudo(TipoTransicaoDigital, ClasseEventoDiscreto,
		"Mudanca de estado de uma entrada discreta.",
		func() *contratov1.TransicaoDigital { return &contratov1.TransicaoDigital{} },
		func(doFio *contratov1.TransicaoDigital) (ConteudoDecodificado, error) {
			return TransicaoDigital{
				Endereco: enderecoDe(doFio.GetEndereco()),
				Ativo:    doFio.GetAtivo(),
			}, nil
		})
}
