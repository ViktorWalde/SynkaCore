// Package aquisicao define o envelope canonico e as classes de dado do SynkaCore.
//
// Este package e o nucleo do dominio: nao conhece HTTP, banco, arquivo nem
// relogio. Tudo que entra no sistema — por rede, por barramento serial ou pelo
// no em software — vira um Envelope construido aqui, e e validado UMA vez, aqui.
package aquisicao

import "time"

// ClasseDeDado classifica o dado pela GARANTIA que ele exige, nao pelo assunto.
//
// A alternativa recusada era "modelagem generica de eventos temporais", que na
// pratica termina numa tabela com uma coluna JSONB onde tudo cabe e nada e
// validado. E nao e generico de fato: uma amostra de temperatura e uma parada de
// maquina tem requisitos OPOSTOS — a primeira tolera perda, a segunda nao tem
// vizinha que a substitua.
//
// Cada classe carrega CINCO politicas, definidas aqui e em nenhum outro lugar:
// garantia de entrega, saturacao de buffer, orcamento de latencia, durabilidade
// local antes da confirmacao e retencao. Nenhum adaptador decide qualquer uma
// delas. E a abstracao que mais se paga do projeto justamente porque concentra
// cinco decisoes que, espalhadas, divergiriam.
type ClasseDeDado uint8

const (
	// ClasseAmostra e telemetria periodica: o valor de uma grandeza num instante.
	//
	// Tolera perda porque a proxima amostra chega logo e carrega quase a mesma
	// informacao. Perder uma leitura de temperatura entre duas vizinhas nao altera
	// nenhuma conclusao.
	ClasseAmostra ClasseDeDado = iota + 1

	// ClasseEventoDiscreto e um fato que ocorreu: parada de maquina, alarme,
	// pulso de contagem.
	//
	// NAO tolera perda silenciosa: um evento perdido nao tem vizinho que o
	// substitua, e a contagem fica permanentemente errada sem que ninguem perceba.
	ClasseEventoDiscreto
)

// primeiraClasse e ultimaClasse delimitam a faixa valida.
//
// Declaradas a partir das proprias constantes, e nao como literais, para que
// acrescentar uma classe nova nao exija lembrar de atualizar um numero solto.
const (
	primeiraClasse = ClasseAmostra
	ultimaClasse   = ClasseEventoDiscreto
)

// GarantiaDeEntrega expressa quantas vezes o dado precisa chegar para que o
// sistema esteja correto.
type GarantiaDeEntrega uint8

const (
	// EntregaMelhorEsforco aceita perda. Nao ha retransmissao dedicada.
	EntregaMelhorEsforco GarantiaDeEntrega = iota + 1

	// EntregaAoMenosUmaVez exige retransmissao ate confirmacao.
	//
	// Duplicata e consequencia ESPERADA e e removida pela chave de idempotencia.
	// Nao existe entrega exatamente-uma-vez no transporte; existe entrega
	// ao-menos-uma-vez mais deduplicacao no destino.
	EntregaAoMenosUmaVez
)

// PoliticaDeSaturacao declara o que fazer quando o buffer de origem enche porque
// o gateway esta fora do ar ou saturado.
//
// "A origem bufferiza" sem politica declarada nao e projeto: o buffer VAI encher,
// e o comportamento nessa hora precisa ser uma decisao, nao um acidente.
type PoliticaDeSaturacao uint8

const (
	// SaturacaoDescartarMaisAntigo descarta a amostra mais antiga para abrir espaco.
	//
	// Correto para telemetria: em buffer cheio, o dado recente vale mais que o
	// antigo. O descarte e contabilizado e reportado na telemetria de saude, entao
	// a perda e conhecida em numeros mesmo sendo aceita.
	SaturacaoDescartarMaisAntigo PoliticaDeSaturacao = iota + 1

	// SaturacaoRegistrarLacuna proibe descarte silencioso.
	//
	// Se o descarte for inevitavel, a origem emite um marcador de lacuna informando
	// quantos itens se perderam e em que intervalo. A perda passa a ser um fato
	// VISIVEL no dado, e nao um buraco invisivel num relatorio.
	//
	// E a diferenca pratica entre um sistema que mente e um que admite o que nao sabe.
	SaturacaoRegistrarLacuna
)

// DurabilidadeLocal declara se a origem grava em meio nao volatil ANTES de
// considerar a mensagem entregue.
//
// A pergunta que esta politica responde: a origem grava sempre, ou so quando o
// gateway esta inacessivel? A resposta certa difere por classe, e por isso ela
// mora aqui junto das outras quatro.
type DurabilidadeLocal uint8

const (
	// DurabilidadeEmMemoria mantem em RAM e so grava ao saturar.
	//
	// Correto para telemetria: um reinicio da origem custa segundos de dado que a
	// proxima amostra praticamente repoe, e gravar cada amostra desgastaria a
	// midia sem comprar nada.
	DurabilidadeEmMemoria DurabilidadeLocal = iota + 1

	// DurabilidadeEmDisco grava antes de confirmar, sempre.
	//
	// Evento discreto precisa sobreviver a um reinicio da origem, nao apenas a uma
	// queda de rede. Sem isso, a autonomia de buffer protegeria contra o modo de
	// falha errado.
	DurabilidadeEmDisco
)

// PoliticaDeRetencao declara por quanto tempo o dado bruto e guardado, se ele
// pode ser agregado, e por quanto tempo a forma final sobrevive.
//
// Os valores sao PADROES dimensionados para o cenario alvo, nunca constantes
// embutidas no caminho de execucao: uma planta menor roda o mesmo binario com
// outro arquivo de configuracao. Eles vivem aqui para que a classe continue
// sendo o unico lugar onde a politica e decidida.
type PoliticaDeRetencao struct {
	// Bruta e por quanto tempo o registro original e mantido sem agregacao.
	Bruta time.Duration

	// Agregavel informa se consolidar a serie preserva o significado do dado.
	// Falso significa que o registro e contado individualmente por um relatorio,
	// e agrega-lo destruiria a resposta.
	Agregavel bool

	// Final e por quanto tempo a forma resultante — agregada ou integral —
	// sobrevive.
	Final time.Duration
}

// Orcamentos de latencia por classe.
//
// Sao o prazo maximo que uma mensagem pode esperar no buffer de origem antes de
// ser despachada, e portanto definem a janela de lote. Existem porque lote e
// pre-requisito de viabilidade: sem ele, o fsync sozinho consome toda a
// capacidade do disco do gateway no cenario dimensionado.
//
// Valores diferentes por classe, e nao um unico global, porque o trade-off e
// oposto nos dois casos: alarme parado 5 segundos num buffer e alarme inutil, e
// telemetria de tendencia despachada a cada 200 ms desperdica I/O sem ninguem
// notar ganho. Um valor global estaria errado nos dois sentidos ao mesmo tempo.
const (
	// latenciaMaximaDeAmostra favorece eficiencia: agrupa bastante antes de gravar.
	latenciaMaximaDeAmostra = 5 * time.Second

	// latenciaMaximaDeEventoDiscreto favorece reatividade: o operador precisa ver
	// a parada acontecer, nao saber dela depois.
	latenciaMaximaDeEventoDiscreto = 200 * time.Millisecond
)

// Padroes de retencao por classe.
//
// O volume esta concentrado exatamente na classe que tolera agregacao, e a
// classe que a regulacao exige guardar por mais tempo e a de menor volume. As
// duas exigencias nao competem — e e por isso que essa divisao funciona.
const (
	retencaoBrutaDeAmostra = 7 * 24 * time.Hour
	retencaoFinalDeAmostra = 365 * 24 * time.Hour

	retencaoDeEventoDiscreto = 5 * 365 * 24 * time.Hour
)

// GarantiaDeEntrega devolve a garantia exigida por esta classe.
//
// Sem clausula default de proposito. O linter exhaustive esta configurado com
// default-signifies-exhaustive: false, de modo que acrescentar uma ClasseDeDado
// e reprovado ate que a politica dela seja decidida aqui. Uma versao com default
// pareceria uma trava e nao seria: a classe nova cairia no padrao em silencio,
// que e exatamente como a classe mais critica de um sistema entra pela porta que
// o autor deixou aberta.
//
// O retorno final e alcancavel apenas com ClasseDeDado invalida, que NovoEnvelope
// ja recusa. Errar para o lado de preservar dado e sempre o erro mais barato.
func (c ClasseDeDado) GarantiaDeEntrega() GarantiaDeEntrega {
	switch c {
	case ClasseAmostra:
		return EntregaMelhorEsforco
	case ClasseEventoDiscreto:
		return EntregaAoMenosUmaVez
	}
	return EntregaAoMenosUmaVez
}

// PoliticaDeSaturacao devolve o que fazer quando o buffer de origem enche.
func (c ClasseDeDado) PoliticaDeSaturacao() PoliticaDeSaturacao {
	switch c {
	case ClasseAmostra:
		return SaturacaoDescartarMaisAntigo
	case ClasseEventoDiscreto:
		return SaturacaoRegistrarLacuna
	}
	return SaturacaoRegistrarLacuna
}

// LatenciaMaximaDeEntrega devolve o prazo maximo entre a amostragem na origem e
// o despacho para o gateway. Define a janela de lote desta classe.
func (c ClasseDeDado) LatenciaMaximaDeEntrega() time.Duration {
	switch c {
	case ClasseAmostra:
		return latenciaMaximaDeAmostra
	case ClasseEventoDiscreto:
		return latenciaMaximaDeEventoDiscreto
	}
	return latenciaMaximaDeEventoDiscreto
}

// DurabilidadeLocal devolve se a origem grava em meio nao volatil antes de
// considerar a mensagem entregue.
func (c ClasseDeDado) DurabilidadeLocal() DurabilidadeLocal {
	switch c {
	case ClasseAmostra:
		return DurabilidadeEmMemoria
	case ClasseEventoDiscreto:
		return DurabilidadeEmDisco
	}
	return DurabilidadeEmDisco
}

// PoliticaDeRetencao devolve os padroes de retencao desta classe.
func (c ClasseDeDado) PoliticaDeRetencao() PoliticaDeRetencao {
	switch c {
	case ClasseAmostra:
		return PoliticaDeRetencao{
			Bruta:     retencaoBrutaDeAmostra,
			Agregavel: true,
			Final:     retencaoFinalDeAmostra,
		}
	case ClasseEventoDiscreto:
		return PoliticaDeRetencao{
			Bruta:     retencaoDeEventoDiscreto,
			Agregavel: false,
			Final:     retencaoDeEventoDiscreto,
		}
	}
	return PoliticaDeRetencao{
		Bruta:     retencaoDeEventoDiscreto,
		Agregavel: false,
		Final:     retencaoDeEventoDiscreto,
	}
}

// String devolve o nome estavel da classe, usado em log, metrica e no modelo de
// leitura. Estavel em ingles pelo mesmo motivo de falha.Categoria.String.
func (c ClasseDeDado) String() string {
	switch c {
	case ClasseAmostra:
		return "sample"
	case ClasseEventoDiscreto:
		return "discrete_event"
	}
	return "unknown"
}

// Valida informa se a classe e uma das declaradas.
func (c ClasseDeDado) Valida() bool {
	return c >= primeiraClasse && c <= ultimaClasse
}
