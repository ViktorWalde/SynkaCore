// Package no implementa a origem do dado: o que numa instalacao com hardware
// seria o equipamento embarcado, e aqui e um processo em software.
//
// Ele fala com o gateway pelo MESMO contrato de fio que um dispositivo real
// usaria. O gateway nao tem como distinguir os dois — e essa e a propriedade que
// torna o desenvolvimento sem hardware honesto em vez de conveniente: o caminho
// exercitado aqui e o caminho que rodara em producao.
package no

import (
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
)

// itemBufferizado e um envelope aguardando despacho, com a classe que decide o
// que fazer com ele quando o espaco acabar.
type itemBufferizado struct {
	envelope *contratov1.Envelope
	classe   aquisicao.ClasseDeDado
}

// Buffer guarda o que ainda nao foi confirmado pelo gateway.
//
// A politica de saturacao NAO e escolha deste tipo: ela vem da ClasseDeDado, que
// e o unico lugar do sistema onde essa decisao mora. O buffer apenas a executa.
//
// "A origem bufferiza" sem politica declarada nao e projeto. O buffer VAI encher —
// e questao de tempo, nao de possibilidade —, e o comportamento nessa hora precisa
// ser uma decisao tomada com calma, e nao um acidente descoberto durante uma queda.
type Buffer struct {
	mutex      sync.Mutex
	itens      []itemBufferizado
	capacidade int

	// lacuna acumula o que foi descartado desde o ultimo despacho de marcador.
	//
	// Acumulada, e nao emitida a cada descarte: durante uma saturacao severa,
	// emitir um marcador por item descartado geraria mais trafego que o dado que
	// nao coube — e o marcador, sendo evento discreto, tem prioridade de entrega.
	// A saturacao pioraria a si mesma.
	lacuna LacunaAcumulada
}

// LacunaAcumulada e a contabilidade do que foi perdido.
//
// Existe para que perda NUNCA seja silenciosa. Em vez de um buraco invisivel num
// relatorio, o descarte vira um fato visivel no dado, com intervalo e contagem. E
// a diferenca pratica entre um sistema que mente e um que admite o que nao sabe.
type LacunaAcumulada struct {
	Registros         uint64
	PrimeiraSequencia uint64
	UltimaSequencia   uint64
}

// Vazia informa se nao ha perda pendente de reportar.
func (l LacunaAcumulada) Vazia() bool { return l.Registros == 0 }

// NovoBuffer constroi o buffer com a capacidade indicada, em itens.
//
// Capacidade em ITENS, e nao em bytes, porque o limite que importa numa origem
// embarcada e o numero de estruturas alocadas estaticamente, nao o total ocupado.
// Um limite em bytes exigiria medir cada serializacao antes de decidir se cabe, e
// pagaria esse custo em todo item que couber sem problema.
func NovoBuffer(capacidade int) *Buffer {
	if capacidade < 1 {
		capacidade = 1
	}
	return &Buffer{itens: make([]itemBufferizado, 0, capacidade), capacidade: capacidade}
}

// Acrescentar guarda um envelope, aplicando a politica de saturacao se preciso.
//
// A ordem de descarte, quando o espaco acaba, e a parte que merece atencao:
//
//  1. Descarta a AMOSTRA mais antiga. Amostra tolera perda por construcao — a
//     proxima chega logo e carrega quase a mesma informacao —, e em buffer cheio o
//     dado recente vale mais que o antigo.
//  2. So se nao houver amostra nenhuma, descarta o EVENTO mais antigo, e registra
//     a lacuna.
//
// A prioridade importa porque as duas classes convivem no mesmo buffer. Descartar
// pela idade pura sacrificaria uma parada de maquina para preservar uma leitura de
// temperatura — invertendo exatamente a garantia que as classes existem para
// declarar.
func (b *Buffer) Acrescentar(envelope *contratov1.Envelope, classe aquisicao.ClasseDeDado) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if len(b.itens) >= b.capacidade {
		b.abrirEspaco()
	}
	b.itens = append(b.itens, itemBufferizado{envelope: envelope, classe: classe})
}

// abrirEspaco remove um item segundo a politica de saturacao. Exige o mutex.
func (b *Buffer) abrirEspaco() {
	indiceDaAmostraMaisAntiga := -1
	for indice, item := range b.itens {
		if item.classe.PoliticaDeSaturacao() == aquisicao.SaturacaoDescartarMaisAntigo {
			indiceDaAmostraMaisAntiga = indice
			break
		}
	}

	if indiceDaAmostraMaisAntiga >= 0 {
		// Amostra sai em silencio: a politica da classe declara que essa perda e
		// aceitavel, e ela ainda aparece contabilizada na telemetria de saude.
		b.itens = append(b.itens[:indiceDaAmostraMaisAntiga], b.itens[indiceDaAmostraMaisAntiga+1:]...)
		b.lacuna.Registros++
		return
	}

	// So restam eventos discretos. Um deles precisa sair, e a perda NAO pode ser
	// silenciosa: registra-se o intervalo para que o marcador de lacuna a denuncie.
	descartado := b.itens[0]
	b.itens = b.itens[1:]

	sequencia := descartado.envelope.GetNumeroDeSequencia()
	if b.lacuna.Registros == 0 || sequencia < b.lacuna.PrimeiraSequencia {
		b.lacuna.PrimeiraSequencia = sequencia
	}
	if sequencia > b.lacuna.UltimaSequencia {
		b.lacuna.UltimaSequencia = sequencia
	}
	b.lacuna.Registros++
}

// Drenar devolve ate `limite` itens e os remove do buffer.
//
// Removidos ANTES da confirmacao de proposito. Se o despacho falhar, quem chamou
// devolve o lote com Devolver — e essa e a assinatura que torna impossivel
// despachar duas vezes o mesmo item por esquecimento de remove-lo.
func (b *Buffer) Drenar(limite int) []*contratov1.Envelope {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if limite > len(b.itens) {
		limite = len(b.itens)
	}
	if limite == 0 {
		return nil
	}

	drenados := make([]*contratov1.Envelope, 0, limite)
	for _, item := range b.itens[:limite] {
		drenados = append(drenados, item.envelope)
	}
	b.itens = b.itens[limite:]
	return drenados
}

// Devolver recoloca no INICIO do buffer itens que nao puderam ser despachados.
//
// No inicio, e nao no fim, para preservar a ordem de sequencia. Devolver ao fim
// faria o lote seguinte sair fora de ordem, e a confirmacao por faixa contigua —
// "duravel ate a sequencia N" — deixaria de ser expressavel.
func (b *Buffer) Devolver(envelopes []*contratov1.Envelope, classes []aquisicao.ClasseDeDado) {
	if len(envelopes) == 0 {
		return
	}

	b.mutex.Lock()
	defer b.mutex.Unlock()

	devolvidos := make([]itemBufferizado, 0, len(envelopes))
	for indice, envelope := range envelopes {
		classe := aquisicao.ClasseAmostra
		if indice < len(classes) {
			classe = classes[indice]
		}
		devolvidos = append(devolvidos, itemBufferizado{envelope: envelope, classe: classe})
	}

	b.itens = append(devolvidos, b.itens...)

	// A devolucao pode estourar a capacidade — o buffer continuou recebendo
	// enquanto o despacho estava em curso. Aplicar a politica agora e o que impede
	// o buffer de crescer sem limite justamente durante uma indisponibilidade
	// prolongada, que e quando ele mais e exercitado.
	for len(b.itens) > b.capacidade {
		b.abrirEspaco()
	}
}

// TomarLacuna devolve a lacuna acumulada e zera a contabilidade.
//
// Zerar na leitura garante que cada perda seja reportada UMA vez. Reportar de novo
// inflaria o numero de registros perdidos a cada despacho, e um indicador que
// cresce sozinho e pior que nenhum.
func (b *Buffer) TomarLacuna() LacunaAcumulada {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	lacuna := b.lacuna
	b.lacuna = LacunaAcumulada{}
	return lacuna
}

// Ocupacao devolve quantos itens estao aguardando despacho.
//
// Reportada na telemetria de saude para que a saturacao possa ser alarmada ANTES
// de acontecer, e nao descoberta pelo marcador de lacuna depois.
func (b *Buffer) Ocupacao() int {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return len(b.itens)
}

// BytesEstimados devolve o tamanho aproximado do que esta bufferizado.
func (b *Buffer) BytesEstimados() int {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	var total int
	for _, item := range b.itens {
		total += proto.Size(item.envelope)
	}
	return total
}

// TemEventoDiscreto informa se ha evento discreto aguardando despacho.
//
// Existe para que o laco de despacho saiba QUANDO despachar sem ter que inspecionar
// o buffer por fora. Evento discreto tem orcamento de latencia muito mais apertado
// que amostra — um alarme parado cinco segundos num buffer e um alarme inutil —, e
// essa e a pergunta que decide se o lote sai agora ou espera encher.
func (b *Buffer) TemEventoDiscreto() bool {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	for _, item := range b.itens {
		if item.classe == aquisicao.ClasseEventoDiscreto {
			return true
		}
	}
	return false
}

// ClassesDe devolve as classes dos envelopes indicados, na mesma ordem.
//
// Serve a Devolver: quem despachou precisa recolocar os itens com a classe certa,
// senao um evento discreto devolvido viraria amostra e passaria a ser descartavel
// em silencio na proxima saturacao.
func ClassesDe(envelopes []*contratov1.Envelope) []aquisicao.ClasseDeDado {
	classes := make([]aquisicao.ClasseDeDado, 0, len(envelopes))
	for _, envelope := range envelopes {
		classes = append(classes, classeDoEnvelope(envelope))
	}
	return classes
}

// classeDoEnvelope resolve a classe de dado a partir do conteudo do envelope.
//
// Descoberta por REFLEXAO sobre a anotacao do contrato, e nao por um switch sobre
// o oneof. A anotacao classe_de_dado esta declarada uma unica vez, no .proto, e as
// duas pontas a leem de la — e isso que impede a origem e o gateway de discordarem
// sobre o que pode ser descartado, que e a especie de divergencia que so aparece no
// dia da falha.
func classeDoEnvelope(envelope *contratov1.Envelope) aquisicao.ClasseDeDado {
	reflexo := envelope.ProtoReflect()

	campo := reflexo.WhichOneof(reflexo.Descriptor().Oneofs().ByName(
		protoreflect.Name(aquisicao.NomeDoOneofDeConteudo)))
	if campo == nil {
		return aquisicao.ClasseEventoDiscreto
	}

	opcoes, temOpcoes := campo.Message().Options().(*descriptorpb.MessageOptions)
	if !temOpcoes {
		return aquisicao.ClasseEventoDiscreto
	}

	classe, temClasse := proto.GetExtension(opcoes, contratov1.E_ClasseDeDado).(contratov1.ClasseDeDado)
	if !temClasse {
		return aquisicao.ClasseEventoDiscreto
	}

	switch classe {
	case contratov1.ClasseDeDado_CLASSE_DE_DADO_AMOSTRA:
		return aquisicao.ClasseAmostra
	case contratov1.ClasseDeDado_CLASSE_DE_DADO_EVENTO_DISCRETO:
		return aquisicao.ClasseEventoDiscreto
	case contratov1.ClasseDeDado_CLASSE_DE_DADO_NAO_ESPECIFICADO:
		// Conteudo sem anotacao de classe cai na garantia mais forte. Errar para o
		// lado de preservar dado e sempre o erro mais barato.
		return aquisicao.ClasseEventoDiscreto
	}
	return aquisicao.ClasseEventoDiscreto
}
