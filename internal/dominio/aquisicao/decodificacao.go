package aquisicao

import (
	"google.golang.org/protobuf/proto"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const operacaoDecodificarConteudo = "aquisicao.decodificarConteudo"

// decodificarConteudo e a UNICA funcao de decodificacao de conteudo do dominio.
//
// Nenhum tipo chama proto.Unmarshal direto. Concentrar aqui garante que as opcoes
// de decodificacao sejam as mesmas para todo tipo — duas politicas de
// decodificacao divergentes produziriam desacordo sobre o que e um conteudo
// valido, que e a classe de defeito que este projeto trata como inaceitavel.
//
// Sobre este package importar o contrato gerado: o dominio nao conhece HTTP,
// banco, arquivo nem relogio, e continua nao conhecendo. O que ele conhece e o
// CONTRATO — que e o vocabulario do dominio, apenas gerado em vez de escrito a
// mao. Reescrever essas estruturas a mao para "manter a pureza" criaria um modelo
// paralelo que divergiria do fio, que e exatamente o defeito que ter um contrato
// como fonte unica existe para impedir.
func decodificarConteudo(bruto []byte, destino proto.Message, descricao string) error {
	// DiscardUnknown fica FALSO de proposito, ao contrario do instinto de "recusar
	// o que nao conheco". O gateway aceita todas as versoes ja publicadas: uma
	// origem com firmware mais novo manda campos que este binario nao conhece, e
	// descarta-los apagaria dado que o proximo gateway saberia ler. Preservados
	// como campos desconhecidos, eles sobrevivem ao ciclo e voltam intactos na
	// reserializacao do diario.
	opcoes := proto.UnmarshalOptions{
		DiscardUnknown: false,

		// A profundidade limita mensagens aninhadas recursivamente, que sao um
		// vetor barato de exaustao de pilha vindo de uma origem comprometida.
		// O contrato tem no maximo tres niveis; cem e folga larga.
		RecursionLimit: 100,
	}
	if err := opcoes.Unmarshal(bruto, destino); err != nil {
		return falha.Envolver(falha.CategoriaEntradaInvalida,
			operacaoDecodificarConteudo, descricao+" malformado", err)
	}
	return nil
}

// definirConteudo monta a definicao de catalogo de um tipo de conteudo.
//
// Existe porque as definicoes compartilhavam um ESQUELETO identico: alocar a
// mensagem do contrato, decodificar, envolver o erro, converter para o dominio.
// Repetido oito vezes, esse esqueleto era duplicacao de verdade — e o linter dupl
// a acusou. Nao a duplicacao de conceito, que seria pior, mas a de mecanica, que
// e a que diverge em silencio: bastaria alguem esquecer de tratar o erro numa das
// copias.
//
// O que NAO foi unificado, e nao deve ser: os tipos de dominio, as classes de dado
// e as validacoes de cada conteudo. Eles sao estruturalmente parecidos e
// semanticamente distintos, e fundi-los num tipo generico apagaria exatamente as
// diferencas que o sistema existe para respeitar.
//
// converter recebe a mensagem ja decodificada e devolve o valor de dominio,
// aplicando as validacoes especificas daquele conteudo. E ali, e so ali, que cada
// tipo difere.
func definirConteudo[M proto.Message](
	tipo TipoDeConteudo,
	classe ClasseDeDado,
	descricao string,
	novaMensagem func() M,
	converter func(M) (ConteudoDecodificado, error),
) DefinicaoDeConteudo {
	return DefinicaoDeConteudo{
		Tipo:      tipo,
		Classe:    classe,
		Descricao: descricao,
		Decodificar: func(bruto []byte) (ConteudoDecodificado, error) {
			doFio := novaMensagem()
			if err := decodificarConteudo(bruto, doFio, "conteudo de "+string(tipo)); err != nil {
				return nil, err
			}
			return converter(doFio)
		},
	}
}
