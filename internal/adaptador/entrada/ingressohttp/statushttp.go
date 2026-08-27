package ingressohttp

import (
	"net/http"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

// statusDe traduz a taxonomia de falha do dominio para o vocabulario do HTTP.
//
// Este e o UNICO mapeador do adaptador HTTP. Nenhum handler decide status por
// conta propria, e nenhum condicional sobre tipo de erro aparece fora daqui — e
// assim que uma taxonomia unica continua sendo unica na pratica, e nao apenas na
// documentacao.
//
// Sem clausula default, para que o linter exhaustive cobre uma categoria nova
// aqui no dia em que ela for criada. Uma categoria que caisse no default viraria
// silenciosamente 500, e um erro do chamador seria relatado como defeito nosso.
func statusDe(categoria falha.Categoria) int {
	switch categoria {
	case falha.CategoriaEntradaInvalida:
		// 400 diz a origem para DESCARTAR. Retransmitir conteudo malformado nao
		// adianta, e uma origem que tenta para sempre nunca mais avanca.
		return http.StatusBadRequest

	case falha.CategoriaNaoAutenticado:
		return http.StatusUnauthorized

	case falha.CategoriaPermissaoNegada:
		return http.StatusForbidden

	case falha.CategoriaNaoEncontrado:
		return http.StatusNotFound

	case falha.CategoriaEntregaDuplicada:
		// 200, e nao um codigo de erro. Reentrega e consequencia ESPERADA de
		// entrega ao-menos-uma-vez; relata-la como falha faria a origem
		// retransmitir o que ja esta salvo, para sempre.
		return http.StatusOK

	case falha.CategoriaRecursoEsgotado:
		// 429 acompanhado de Retry-After. E a unica categoria cuja resposta certa
		// e tentar de novo mais tarde.
		return http.StatusTooManyRequests

	case falha.CategoriaIndisponivel:
		return http.StatusServiceUnavailable

	case falha.CategoriaRestritaPorLicenca:
		return http.StatusPaymentRequired

	case falha.CategoriaInterna:
		// 500 diz a origem para RETRANSMITIR. O defeito e nosso, o dado dela e
		// bom, e ela deve guarda-lo ate o gateway se resolver.
		return http.StatusInternalServerError
	}
	return http.StatusInternalServerError
}
