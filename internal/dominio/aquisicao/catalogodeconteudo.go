package aquisicao

import (
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const (
	operacaoNovoCatalogo = "aquisicao.NovoCatalogoDeConteudo"
	operacaoBuscarTipo   = "aquisicao.CatalogoDeConteudo.Buscar"
)

// TipoDeConteudo identifica o que um Envelope transporta.
//
// E o identificador ESTAVEL que viaja no fio. Muda-lo quebra origens em campo, e
// atualizar firmware em campo e operacao de campo, nao refatoracao.
type TipoDeConteudo string

// ValorProjetado e o valor de um campo que um conteudo contribui ao modelo de
// leitura.
//
// A interface e SELADA por um metodo nao exportado: so este package pode
// implementa-la. Isso e aberto/fechado aplicado com discernimento — o sistema e
// aberto para novos TIPOS DE CONTEUDO (basta um arquivo novo), e fechado para
// novos TIPOS DE VALOR, porque o modelo de leitura e contrato publicado consumido
// por dashboard e SQL, e nao pode ganhar tipos por acidente.
type ValorProjetado interface {
	ehValorProjetado()
}

// ValorNumerico projeta uma grandeza continua.
type ValorNumerico float64

// ValorTexto projeta um rotulo ou identificador.
type ValorTexto string

// ValorLogico projeta um estado binario.
type ValorLogico bool

func (ValorNumerico) ehValorProjetado() {}
func (ValorTexto) ehValorProjetado()    {}
func (ValorLogico) ehValorProjetado()   {}

// CampoProjetado e um par nome/valor destinado ao modelo de leitura.
//
// Nome em ingles por convencao de saida: ele vira coluna de banco e rotulo de
// dashboard. Ver falha.Categoria.String.
type CampoProjetado struct {
	Nome  string
	Valor ValorProjetado
}

// ConteudoDecodificado e o conteudo de um Envelope ja interpretado.
//
// CamposProjetados existe para que haja UM projetor generico em vez de um projetor
// por tipo de conteudo — que seria exatamente a duplicacao que o projeto proibe.
// Cada tipo declara o que contribui ao modelo de leitura; o projetor nao conhece
// tipo nenhum, e acrescentar um tipo nao o modifica.
type ConteudoDecodificado interface {
	Tipo() TipoDeConteudo
	CamposProjetados() []CampoProjetado
}

// DefinicaoDeConteudo descreve tudo que o sistema precisa saber sobre um tipo.
//
// Decodificar e a UNICA funcao por tipo, usada tanto na ingestao (validar) quanto
// na projecao (interpretar). NAO existe um Validar separado, de proposito: duas
// funcoes lendo o mesmo formato divergem com o tempo e passam a discordar sobre o
// que e um conteudo valido. Validar e decodificar e descartar.
type DefinicaoDeConteudo struct {
	// Tipo e o identificador estavel que viaja no fio.
	Tipo TipoDeConteudo

	// Classe determina as cinco politicas. Ver ClasseDeDado.
	Classe ClasseDeDado

	// Descricao documenta o significado fisico, para o catalogo e o diagnostico.
	Descricao string

	// Decodificar interpreta os bytes canonicos do conteudo.
	Decodificar func(bruto []byte) (ConteudoDecodificado, error)
}

// CatalogoDeConteudo e o conjunto de tipos que este gateway reconhece.
//
// Mecanismo de aberto/fechado do sistema: acrescentar um tipo e criar um arquivo
// novo neste package e somar uma linha na montagem do catalogo, na raiz de
// composicao. Nenhuma logica existente e modificada, e nenhum switch sobre tipo de
// conteudo aparece em qualquer outro lugar do codigo.
//
// O catalogo e construido explicitamente e injetado, em vez de ser um registro
// global preenchido por init(): estado global torna o teste dependente de ordem de
// importacao e esconde a composicao real do sistema.
type CatalogoDeConteudo struct {
	definicoesPorTipo map[TipoDeConteudo]DefinicaoDeConteudo
}

// NovoCatalogoDeConteudo monta o catalogo, rejeitando definicoes invalidas ou
// repetidas.
//
// A rejeicao de tipo repetido e o invariante de nao-duplicacao sendo aplicado
// MECANICAMENTE, e nao por revisao de codigo: dois arquivos que definam o mesmo
// tipo derrubam o gateway na inicializacao, nao em producao as tres da manha.
func NovoCatalogoDeConteudo(definicoes ...DefinicaoDeConteudo) (*CatalogoDeConteudo, error) {
	catalogo := &CatalogoDeConteudo{
		definicoesPorTipo: make(map[TipoDeConteudo]DefinicaoDeConteudo, len(definicoes)),
	}
	for _, definicao := range definicoes {
		if definicao.Tipo == "" {
			return nil, falha.Nova(falha.CategoriaEntradaInvalida,
				operacaoNovoCatalogo, "definicao de conteudo sem identificador de tipo")
		}
		if !definicao.Classe.Valida() {
			return nil, falha.Nova(falha.CategoriaEntradaInvalida,
				operacaoNovoCatalogo,
				"definicao de conteudo com classe de dado invalida: "+string(definicao.Tipo))
		}
		if definicao.Decodificar == nil {
			return nil, falha.Nova(falha.CategoriaEntradaInvalida,
				operacaoNovoCatalogo,
				"definicao de conteudo sem decodificador: "+string(definicao.Tipo))
		}
		if _, jaDefinido := catalogo.definicoesPorTipo[definicao.Tipo]; jaDefinido {
			return nil, falha.Nova(falha.CategoriaEntradaInvalida,
				operacaoNovoCatalogo,
				"tipo de conteudo definido mais de uma vez: "+string(definicao.Tipo))
		}
		catalogo.definicoesPorTipo[definicao.Tipo] = definicao
	}
	return catalogo, nil
}

// Buscar devolve a definicao de um tipo de conteudo.
func (c *CatalogoDeConteudo) Buscar(tipo TipoDeConteudo) (DefinicaoDeConteudo, error) {
	definicao, encontrado := c.definicoesPorTipo[tipo]
	if !encontrado {
		return DefinicaoDeConteudo{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoBuscarTipo, "tipo de conteudo desconhecido: "+string(tipo))
	}
	return definicao, nil
}

// Tipos devolve os tipos reconhecidos, para exposicao do contrato e diagnostico.
func (c *CatalogoDeConteudo) Tipos() []TipoDeConteudo {
	tipos := make([]TipoDeConteudo, 0, len(c.definicoesPorTipo))
	for tipo := range c.definicoesPorTipo {
		tipos = append(tipos, tipo)
	}
	return tipos
}
