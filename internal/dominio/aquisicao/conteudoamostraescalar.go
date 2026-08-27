package aquisicao

import (
	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
)

// TipoAmostraEscalar e o valor de uma grandeza continua num instante.
//
// O identificador e EXATAMENTE o nome do campo no `oneof conteudo` do contrato.
// Nao e coincidencia nem convencao frouxa: o codec descobre o tipo por reflexao
// sobre o descritor do protobuf, entao divergir aqui quebraria a resolucao — e o
// teste de cobertura do catalogo reprova antes de qualquer implantacao.
const TipoAmostraEscalar TipoDeConteudo = "amostra_escalar"

// AmostraEscalar e o exemplo canonico de ClasseAmostra.
//
// UM tipo serve todas as grandezas escalares — temperatura, pressao, vazao, peso,
// nivel, corrente. Tipos separados seriam estruturas identicas com nomes
// diferentes: a duplicacao que o projeto proibe. A diferenca entre elas nao e
// estrutural, e semantica — e semantica e configuracao do ponto de medicao, nao
// formato de mensagem.
type AmostraEscalar struct {
	Endereco EnderecoDeCanal

	// Valor na unidade declarada pelo descritor do canal. A origem reporta o
	// numero; o gateway resolve o que ele significa.
	Valor float32
}

// Tipo implementa ConteudoDecodificado.
func (a AmostraEscalar) Tipo() TipoDeConteudo { return TipoAmostraEscalar }

// CamposProjetados implementa ConteudoDecodificado.
func (a AmostraEscalar) CamposProjetados() []CampoProjetado {
	return append(camposDoEndereco(a.Endereco),
		CampoProjetado{Nome: "value", Valor: ValorNumerico(a.Valor)},
	)
}

// DefinicaoDeAmostraEscalar devolve a definicao de catalogo deste tipo.
func DefinicaoDeAmostraEscalar() DefinicaoDeConteudo {
	return definirConteudo(TipoAmostraEscalar, ClasseAmostra,
		"Valor de uma grandeza continua num instante, enderecado por canal.",
		func() *contratov1.AmostraEscalar { return &contratov1.AmostraEscalar{} },
		func(doFio *contratov1.AmostraEscalar) (ConteudoDecodificado, error) {
			return AmostraEscalar{
				Endereco: enderecoDe(doFio.GetEndereco()),
				Valor:    doFio.GetValor(),
			}, nil
		})
}
