package aquisicao

// NomeDoOneofDeConteudo e o nome do `oneof` do contrato que carrega o conteudo de
// um envelope.
//
// Declarado aqui, e nao repetido em cada lugar que faz reflexao sobre o descritor,
// porque um literal solto repetido e o primeiro sintoma de uma regra prestes a ser
// reescrita em dois lugares e ficar diferente nos dois.
const NomeDoOneofDeConteudo = "conteudo"

// TodasAsDefinicoes devolve o inventario completo dos tipos de conteudo que este
// gateway sabe interpretar.
//
// Este e o ponto de extensao aberto/fechado do sistema. Acrescentar um tipo e:
//
//  1. acrescentar a mensagem ao `oneof conteudo` do contrato;
//  2. criar um arquivo conteudo*.go neste package;
//  3. somar uma linha nesta lista.
//
// Nenhuma logica existente e modificada, e nenhum switch sobre tipo de conteudo
// aparece em lugar nenhum do codigo — nem no codec, que descobre o tipo por
// reflexao sobre o descritor do protobuf.
//
// A lista mora aqui, e nao na raiz de composicao, porque ela nao e WIRING: e o
// inventario do proprio catalogo. A raiz continua sendo quem decide se monta o
// catalogo completo ou um subconjunto. Se ela mantivesse a lista, o teste que
// confere cobertura contra o contrato precisaria de uma segunda copia — e duas
// listas do mesmo conjunto divergem, que e precisamente o defeito que a
// verificacao existe para impedir.
func TodasAsDefinicoes() []DefinicaoDeConteudo {
	return []DefinicaoDeConteudo{
		DefinicaoDeAmostraEscalar(),
		DefinicaoDeLeituraDeContador(),
		DefinicaoDeTransicaoDigital(),
		DefinicaoDeAmostraAgregada(),
		DefinicaoDeMudancaDeEstadoDeMaquina(),
		DefinicaoDeSaudeDaOrigem(),
		DefinicaoDeLacunaDeBuffer(),
		DefinicaoDeDescritorDaOrigem(),
	}
}
