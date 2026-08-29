// Package falha define a ÚNICA taxonomia de erros do SynkaCore.
//
// Regra de nao-duplicacao: nenhuma outra camada declara seu proprio conjunto de
// categorias de erro. Adaptadores traduzem esta taxonomia para o vocabulario do
// seu transporte (status HTTP, codigo de saida) em UM unico mapeador por
// adaptador — nunca com condicionais espalhados por handler.
//
// Falhas ESPERADAS de dominio (conteudo invalido, entrega duplicada, saturacao)
// sao representadas como valor de retorno, nunca como panico. Panico fica para o
// que e genuinamente excepcional: defeito de programacao ou invariante do proprio
// gateway violado. O argumento decisivo nao e desempenho, e volume de log: num
// equipamento que ninguem visita, inundar o disco com rastro de pilha de erro
// esperado custaria a trilha de auditoria.
package falha

import (
	"errors"
	"fmt"
)

// Categoria classifica a natureza de uma falha, independente de transporte.
//
// CategoriaInterna e o valor zero de proposito: qualquer erro nao classificado
// degrada para "interna", que e a resposta segura — nunca vaza detalhe ao
// chamador e nunca e confundida com uma falha esperada.
type Categoria uint8

const (
	// CategoriaInterna indica defeito do proprio gateway. Nao e culpa do chamador.
	CategoriaInterna Categoria = iota

	// CategoriaEntradaInvalida indica conteudo malformado ou que viola invariante
	// de dominio. Retentar sem alterar a mensagem nao vai funcionar.
	CategoriaEntradaInvalida

	// CategoriaNaoAutenticado indica credencial ausente, expirada ou nao reconhecida.
	CategoriaNaoAutenticado

	// CategoriaPermissaoNegada indica credencial valida cujo portador nao pode
	// executar a operacao. Distinta de CategoriaNaoAutenticado de proposito: uma
	// pede nova credencial, a outra pede autorizacao.
	CategoriaPermissaoNegada

	// CategoriaNaoEncontrado indica que o recurso referenciado nao existe.
	CategoriaNaoEncontrado

	// CategoriaEntregaDuplicada indica reentrega ja observada, detectada pela
	// chave de idempotencia.
	//
	// NAO e anomalia: entrega ao-menos-uma-vez torna a duplicata consequencia
	// esperada do desenho store-and-forward. O chamador deve trata-la como
	// sucesso, e o gateway nao deve alarmar sobre ela.
	CategoriaEntregaDuplicada

	// CategoriaRecursoEsgotado indica contrapressao: o gateway esta saturado.
	// Unica categoria cuja resposta correta e retentar com recuo e jitter.
	CategoriaRecursoEsgotado

	// CategoriaIndisponivel indica dependencia a jusante fora do ar (por exemplo,
	// o banco de consulta). NUNCA se aplica ao diario de ingestao: se o diario
	// falha, e CategoriaInterna, porque a durabilidade foi violada.
	CategoriaIndisponivel

	// CategoriaArmazenamentoEsgotado indica que o diario alcancou o teto de tamanho.
	//
	// DISTINTA de CategoriaRecursoEsgotado, e a distincao existe porque a acao do
	// OPERADOR e oposta, ainda que a da origem seja a mesma nos dois casos —
	// preservar o lote e tentar de novo.
	//
	//	RecursoEsgotado        — o gateway esta ocupado. Passa sozinho, e esperar
	//	                         resolve. Ninguem precisa fazer nada.
	//	ArmazenamentoEsgotado  — o disco encheu. NAO passa sozinho se nao houver
	//	                         projecao consumindo o diario, e esperar so adia. E
	//	                         preciso ligar a projecao, aumentar o teto ou trocar
	//	                         a midia.
	//
	// Colapsar as duas faria um "gateway saturado" no painel esconder um disco cheio,
	// e a planta descobriria a diferenca quando a autonomia das origens acabasse.
	CategoriaArmazenamentoEsgotado

	// CategoriaRestritaPorLicenca indica funcionalidade nao habilitada pela licenca.
	//
	// Invariante do projeto: esta categoria NUNCA pode originar do caminho de
	// aquisicao ou de persistencia. Licenca restringe apresentacao e API
	// acessoria; jamais impede a planta de gravar dado.
	CategoriaRestritaPorLicenca
)

// primeiraCategoria e ultimaCategoria delimitam a faixa declarada.
//
// Derivadas das proprias constantes, e nao escritas como literais, para que
// acrescentar uma categoria nao exija lembrar de atualizar um numero solto — o
// tipo de erro que passa despercebido em revisao.
const (
	primeiraCategoria = CategoriaInterna
	ultimaCategoria   = CategoriaRestritaPorLicenca
)

// String devolve o nome estavel da categoria, usado em log e metrica.
//
// O nome em ingles e deliberado e vale para todo identificador que sai do
// processo: rotulo de metrica, chave de log estruturado e coluna do modelo de
// leitura sao consumidos por Prometheus, Grafana e SQL, cujo vocabulario e
// ingles. Identificador de codigo e portugues; identificador de saida e estavel.
func (c Categoria) String() string {
	switch c {
	case CategoriaInterna:
		return "internal"
	case CategoriaEntradaInvalida:
		return "invalid_input"
	case CategoriaNaoAutenticado:
		return "unauthenticated"
	case CategoriaPermissaoNegada:
		return "permission_denied"
	case CategoriaNaoEncontrado:
		return "not_found"
	case CategoriaEntregaDuplicada:
		return "duplicate_delivery"
	case CategoriaRecursoEsgotado:
		return "resource_exhausted"
	case CategoriaArmazenamentoEsgotado:
		return "storage_exhausted"
	case CategoriaIndisponivel:
		return "unavailable"
	case CategoriaRestritaPorLicenca:
		return "license_restricted"
	}
	return "internal"
}

// Valida informa se a categoria e uma das declaradas.
func (c Categoria) Valida() bool {
	return c >= primeiraCategoria && c <= ultimaCategoria
}

// Erro e a unica implementacao de erro classificado do SynkaCore.
//
// O campo operacao carrega o nome da operacao logica que falhou (por exemplo,
// "ingestao.GravarNoDiario"), permitindo reconstruir o caminho sem rastro de
// pilha — que e justamente o que nao se quer gerar duas mil vezes por segundo.
type Erro struct {
	categoria Categoria
	operacao  string
	mensagem  string
	causa     error
}

// Nova constroi uma falha sem causa subjacente.
func Nova(categoria Categoria, operacao, mensagem string) *Erro {
	return &Erro{categoria: categoria, operacao: operacao, mensagem: mensagem}
}

// Envolver constroi uma falha preservando a causa para inspecao via errors.Is e
// errors.As.
func Envolver(categoria Categoria, operacao, mensagem string, causa error) *Erro {
	return &Erro{categoria: categoria, operacao: operacao, mensagem: mensagem, causa: causa}
}

// Error implementa a interface error. O nome e imposto pela linguagem.
func (e *Erro) Error() string {
	if e.causa == nil {
		return fmt.Sprintf("%s: %s: %s", e.operacao, e.categoria, e.mensagem)
	}
	return fmt.Sprintf("%s: %s: %s: %v", e.operacao, e.categoria, e.mensagem, e.causa)
}

// Unwrap expoe a causa para errors.Is e errors.As. O nome e imposto pela biblioteca padrao.
func (e *Erro) Unwrap() error { return e.causa }

// Categoria devolve a classificacao desta falha.
func (e *Erro) Categoria() Categoria { return e.categoria }

// Operacao devolve a operacao logica que falhou.
func (e *Erro) Operacao() string { return e.operacao }

// CategoriaDe classifica qualquer erro, inclusive os de terceiros.
//
// Este e o UNICO ponto de classificacao de erro do sistema. Adaptadores chamam
// CategoriaDe e mapeiam o resultado; nunca inspecionam o tipo concreto por conta
// propria, o que produziria condicionais divergentes espalhados por handler.
func CategoriaDe(err error) Categoria {
	if err == nil {
		return CategoriaInterna
	}
	var classificado *Erro
	if errors.As(err, &classificado) {
		return classificado.categoria
	}
	return CategoriaInterna
}

// TemCategoria informa se err, ou qualquer erro em sua cadeia, tem a
// classificacao indicada.
func TemCategoria(err error, categoria Categoria) bool {
	return err != nil && CategoriaDe(err) == categoria
}
