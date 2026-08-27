package aquisicao

import (
	"strconv"
	"strings"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
)

// EnderecoDeCanal localiza uma entrada fisica dentro de uma origem.
//
// Tipo proprio, e nao dois uint32 soltos, pela mesma razao que o contrato o
// aninha: a funcao que resolve endereco para ponto de medicao recebe UM argumento
// tipado, e ninguem consegue trocar a ordem de modulo e canal numa chamada. Dois
// inteiros adjacentes numa assinatura compilam, rodam, e atribuem a leitura ao
// ponto errado — em silencio, por anos.
type EnderecoDeCanal struct {
	IndiceDoModulo uint32
	IndiceDoCanal  uint32
}

// enderecoDe converte o endereco do contrato para o tipo de dominio.
//
// Endereco ausente vira o endereco zero, que e legitimo: modulo 0, canal 0
// significa "a propria origem, primeira entrada". Uma origem de canal unico nao
// precisa declarar endereco nenhum.
func enderecoDe(endereco *contratov1.EnderecoDeCanal) EnderecoDeCanal {
	if endereco == nil {
		return EnderecoDeCanal{}
	}
	return EnderecoDeCanal{
		IndiceDoModulo: endereco.GetIndiceDoModulo(),
		IndiceDoCanal:  endereco.GetIndiceDoCanal(),
	}
}

// String devolve a forma textual canonica do endereco, no formato "modulo/canal".
//
// Este e o UNICO formato de serializacao do endereco. Rotulo de metrica, chave de
// resolucao para ponto de medicao e coluna do modelo de leitura usam esta funcao.
func (e EnderecoDeCanal) String() string {
	var construtor strings.Builder
	construtor.Grow(21)
	construtor.WriteString(strconv.FormatUint(uint64(e.IndiceDoModulo), 10))
	construtor.WriteByte('/')
	construtor.WriteString(strconv.FormatUint(uint64(e.IndiceDoCanal), 10))
	return construtor.String()
}

// ConteudoEnderecado e implementado pelos conteudos que se referem a um CANAL
// especifico, e nao ao dispositivo como um todo.
//
// Interface, e nao um switch sobre tipo de conteudo, pelo mesmo motivo que
// sustenta o resto do desenho: um switch seria mais uma lista de tipos, e listas do
// mesmo conjunto divergem. Aqui, um conteudo novo declara se tem endereco
// simplesmente implementando este metodo, e quem resolve ponto de medicao nao
// precisa saber que ele existe.
//
// Nao a implementam, de proposito: saude da origem, lacuna de buffer e descritor.
// Os tres descrevem o DISPOSITIVO, nao um canal — e nao ter endereco e a afirmacao
// correta sobre eles, nao uma omissao.
type ConteudoEnderecado interface {
	EnderecoDoCanal() EnderecoDeCanal
}

// camposDoEndereco devolve a contribuicao do endereco ao modelo de leitura.
//
// Uma funcao, usada por todo conteudo enderecado, em vez de tres linhas repetidas
// em cada um. Repeti-las convidaria a divergencia de nome de coluna entre tipos,
// e o modelo de leitura e contrato consumido por dashboard e SQL.
func camposDoEndereco(endereco EnderecoDeCanal) []CampoProjetado {
	return []CampoProjetado{
		{Nome: "module_index", Valor: ValorNumerico(endereco.IndiceDoModulo)},
		{Nome: "channel_index", Valor: ValorNumerico(endereco.IndiceDoCanal)},
	}
}
