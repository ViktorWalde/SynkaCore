// Package estadooperacional rastreia em que situacao o gateway esta, de forma
// legivel por outra goroutine e por quem consulta a saude.
//
// Os tres estados vem da V1.x do SynkaCore e sao uma das poucas coisas daquele
// desenho que atravessaram a reescrita sem mudar de forma. O que mudou foi ONDE
// eles se aplicam.
//
// Na V1.x eles descreviam o worker de coleta, porque o worker escrevia direto no
// banco e a queda do banco era a queda do sistema. Na V2.0 o caminho de aquisicao
// nao toca no banco de consulta — ele grava no diario, que e local. Entao a queda
// do TimescaleDB deixou de ser capaz de ameacar a aquisicao, e estes estados
// passam a descrever a PROJECAO.
//
// A consequencia pratica e a que interessa: DEGRADADO deixou de significar "o
// sistema pode estar perdendo dado" e passou a significar "o dado esta salvo, mas
// os dashboards estao atrasados". E uma degradacao de qualidade de servico, nao de
// integridade — e o operador de plantao precisa saber a diferenca as tres da
// manha.
package estadooperacional

import (
	"sync"
	"time"
)

// Estado e a situacao corrente da projecao.
type Estado uint8

const (
	// Conectado: a projecao esta alcancando o banco de consulta normalmente.
	Conectado Estado = iota + 1

	// Reconectando: houve falha e ha tentativa em andamento.
	//
	// Estado proprio, e nao um detalhe de Degradado, porque a resposta operacional
	// e diferente: reconectando resolve sozinho na maioria das vezes, e acordar
	// alguem por isso e o caminho mais curto para o alarme ser ignorado.
	Reconectando

	// Degradado: as tentativas se esgotaram e a projecao esta suspensa.
	//
	// O dado continua sendo gravado no diario e nada se perde. O que para e o
	// espelhamento para o banco de consulta.
	Degradado
)

// String devolve o nome estavel do estado, usado em log, metrica e no corpo do
// health check. Ingles por convencao de saida, como falha.Categoria.String.
func (e Estado) String() string {
	switch e {
	case Conectado:
		return "connected"
	case Reconectando:
		return "reconnecting"
	case Degradado:
		return "degraded"
	}
	return "unknown"
}

// Rastreador guarda o estado corrente e desde quando ele vale.
//
// Protegido por mutex, e nao por um campo atomico solo, por uma razao concreta: o
// estado e o instante da mudanca precisam ser lidos JUNTOS. Com dois campos
// atomicos independentes, um leitor poderia pegar o estado novo com o instante
// antigo e relatar "degradado ha 3 horas" no primeiro segundo da degradacao.
//
// O custo e irrelevante: isto e lido por health check e escrito na transicao, nao
// no caminho quente.
type Rastreador struct {
	mutex  sync.RWMutex
	estado Estado
	desde  time.Time

	// aoMudar e notificado apenas quando o estado REALMENTE muda.
	//
	// Existe para que a transicao seja registrada uma vez, e nao a cada ciclo de
	// projecao. Um log que repete a mesma linha a cada dois segundos durante uma
	// queda de tres horas enterra a informacao util e enche o disco de um
	// equipamento que ninguem visita.
	aoMudar func(anterior, atual Estado)
}

// NovoRastreador constroi o rastreador no estado conectado.
//
// Comeca conectado, e nao num estado "desconhecido", porque a primeira falha e
// quem produz informacao. Nascer degradado faria todo gateway saudavel alarmar na
// partida, e um alarme que sempre dispara e um alarme que ninguem le.
func NovoRastreador(agora time.Time, aoMudar func(anterior, atual Estado)) *Rastreador {
	if aoMudar == nil {
		aoMudar = func(Estado, Estado) {}
	}
	return &Rastreador{estado: Conectado, desde: agora.UTC(), aoMudar: aoMudar}
}

// Notificar registra o estado corrente.
//
// Chamar com o estado que ja vale nao produz efeito nenhum: nem novo instante,
// nem notificacao. E o que mantem "desde" significando o inicio da situacao atual,
// e nao a ultima vez que alguem perguntou.
func (r *Rastreador) Notificar(estado Estado, agora time.Time) {
	r.mutex.Lock()
	anterior := r.estado
	if anterior == estado {
		r.mutex.Unlock()
		return
	}
	r.estado = estado
	r.desde = agora.UTC()
	r.mutex.Unlock()

	// Notificado FORA do bloqueio: o observador registra em log, e segurar o mutex
	// durante I/O faria a projecao esperar pelo disco de log.
	r.aoMudar(anterior, estado)
}

// Atual devolve o estado corrente e desde quando ele vale, lidos como um par
// consistente.
func (r *Rastreador) Atual() (Estado, time.Time) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return r.estado, r.desde
}
