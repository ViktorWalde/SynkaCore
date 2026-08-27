// Package relogio separa explicitamente as duas nocoes de tempo que o SynkaCore
// precisa manter distintas.
//
// O problema que este package existe para resolver: em Go, time.Time carrega uma
// leitura monotonica quando vem de time.Now(), mas QUALQUER chamada a .UTC(),
// .Round() ou .Truncate() a DESCARTA silenciosamente. A partir dali, subtrair
// dois instantes passa a usar o relogio de parede — que anda para tras quando o
// NTP corrige ou quando alguem acerta a hora na mao.
//
// Isso e corretude em qualquer sistema, mas aqui e conformidade: nao se pode
// provar quando algo aconteceu se o relogio pode retroceder em silencio. Por isso
// as duas leituras viajam separadas e sao comparaveis entre si (ver
// autoridadedetempo.VerificarDegrauDeRelogio).
//
// A abstracao e uma interface de dois metodos, e nao um objeto de relogio
// completo, porque so existem duas perguntas legitimas a fazer ao tempo. Ela e
// injetada a partir da raiz de composicao; nenhum package de dominio a importa.
package relogio

import "time"

// Relogio fornece as duas leituras de tempo do sistema.
type Relogio interface {
	// Agora devolve o relogio de PAREDE, em UTC. Pode dar degrau: e a leitura que
	// responde "que horas sao", e a unica que serve para carimbar um registro que
	// alguem vai correlacionar com um turno.
	Agora() time.Time

	// Decorrido devolve o tempo MONOTONICO desde a partida do processo. Nunca
	// retrocede e nunca da degrau, mas nao tem relacao com a hora do dia. E a
	// leitura que responde "quanto tempo passou".
	Decorrido() time.Duration
}

// Sistema devolve o relogio real do sistema operacional.
//
// A referencia monotonica e capturada AGORA, na construcao, e nunca mais
// substituida: e o instante em que este processo passou a existir, e todo
// Decorrido() e medido contra ele.
func Sistema() Relogio {
	return &relogioDoSistema{partida: time.Now()}
}

type relogioDoSistema struct {
	// partida guarda time.Now() COM a leitura monotonica intacta. Nada aqui pode
	// chamar .UTC() sobre este campo, sob pena de reintroduzir exatamente o
	// defeito que o package existe para fechar.
	partida time.Time
}

func (r *relogioDoSistema) Agora() time.Time { return time.Now().UTC() }

// Decorrido usa time.Since, que subtrai usando a leitura monotonica preservada em
// partida. Correcao de NTP nao afeta o resultado.
func (r *relogioDoSistema) Decorrido() time.Duration { return time.Since(r.partida) }

// Falso e um relogio controlado, para teste.
//
// Existe para que o degrau de relogio — que e difícil de provocar de verdade e
// impossivel de provocar em integracao continua — seja exercitavel: basta mover
// a parede sem mover o monotonico, que e precisamente o que um acerto de hora faz.
type Falso struct {
	parede     time.Time
	monotonico time.Duration
}

// NovoFalso constroi um relogio controlado ancorado no instante indicado.
func NovoFalso(parede time.Time) *Falso {
	return &Falso{parede: parede.UTC()}
}

// Agora devolve o instante de parede corrente do relogio falso.
func (f *Falso) Agora() time.Time { return f.parede }

// Decorrido devolve o monotonico corrente do relogio falso.
func (f *Falso) Decorrido() time.Duration { return f.monotonico }

// Avancar move as DUAS leituras juntas, que e o comportamento normal do tempo.
func (f *Falso) Avancar(d time.Duration) {
	f.parede = f.parede.Add(d)
	f.monotonico += d
}

// DarDegrau move APENAS o relogio de parede, sem tocar no monotonico.
//
// E o que acontece quando o NTP corrige a hora ou quando alguem a acerta na mao.
// Aceita duracao negativa de proposito: o relogio andando para TRAS e o caso
// perigoso, porque produz dado plausivel e errado.
func (f *Falso) DarDegrau(d time.Duration) {
	f.parede = f.parede.Add(d)
}
