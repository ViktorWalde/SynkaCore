// Package autoridadedetempo estabelece que o GATEWAY e a unica autoridade de
// tempo do sistema.
//
// O problema: o SynkaCore e offline-first, sem NTP externo, e a origem do dado
// nao tem relogio de tempo real com bateria — ao ligar, seu relogio de parede
// comeca em 1970. Uma origem que carimba o proprio instante produz dado correto
// ate a primeira queda de energia, e lixo silenciosamente depois. Numa planta
// industrial, queda de energia e rotina.
//
// Pior: o dado corrompido e PLAUSIVEL. Ninguem percebe ate alguem tentar
// correlacionar uma parada de maquina com um turno.
//
// A solucao: a origem NUNCA afirma saber a hora. Ela reporta apenas tempo
// monotonico desde o boot. O gateway carimba a recepcao com seu proprio relogio e
// ancora a sessao de boot, derivando o instante estimado de amostragem.
//
// Os tres tempos permanecem SEPARADOS e auditaveis, e o estimado jamais
// sobrescreve o bruto:
//
//	tempoLigado          — o que a origem mediu (monotonico, confiavel, sem referencia)
//	instanteObservado    — quando o gateway recebeu (relogio real, mas com latencia)
//	instanteEstimado     — derivado (util para consulta, e explicitamente estimado)
//
// Requisito de hardware que decorre daqui: o gateway PRECISA de relogio de tempo
// real com bateria. Sem ele, o sistema inteiro perde a referencia temporal na
// primeira queda de energia, e nenhum software resolve.
package autoridadedetempo

import (
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const (
	operacaoNovaAncora      = "autoridadedetempo.NovaAncoraDeSessaoDeBoot"
	operacaoEstimar         = "autoridadedetempo.EstimarInstanteDaAmostra"
	operacaoVerificarDegrau = "autoridadedetempo.VerificarDegrauDeRelogio"

	// ToleranciaDeDegrauPadrao e o quanto a parede pode divergir do monotonico
	// antes de a divergencia ser tratada como degrau de relogio.
	//
	// Um segundo e folgado para absorver a granularidade das leituras e o custo de
	// agendamento entre as duas chamadas, e apertado o bastante para pegar
	// qualquer correcao de NTP que valha o nome — que tipicamente salta segundos
	// ou mais quando o desvio acumulou.
	ToleranciaDeDegrauPadrao = time.Second
)

// AncoraDeSessaoDeBoot amarra o tempo monotonico de uma sessao de boot ao relogio
// real do gateway.
//
// Criada na PRIMEIRA mensagem recebida de cada sessao de boot e imutavel depois:
// reancorar a cada mensagem faria a latencia de rede variavel contaminar toda a
// serie, deslocando amostras para frente e para tras sem relacao com a realidade
// fisica.
type AncoraDeSessaoDeBoot struct {
	idDoDispositivo  identidadededispositivo.IDDoDispositivo
	idDaSessaoDeBoot identidadededispositivo.IDDaSessaoDeBoot

	// tempoLigadoDaAncora e o tempo ligado informado na primeira mensagem da sessao.
	tempoLigadoDaAncora time.Duration

	// instanteDaAncora e o relogio de PAREDE do gateway quando essa primeira
	// mensagem foi recebida. E o que da significado de calendario a serie.
	instanteDaAncora time.Time

	// decorridoDaAncora e a leitura MONOTONICA do gateway no mesmo instante.
	//
	// Guardada junto, e nao no lugar da parede, porque as duas juntas sao o que
	// torna um degrau de relogio DETECTAVEL: sozinha, a parede se move sem deixar
	// vestigio; sozinho, o monotonico nao sabe que horas sao.
	decorridoDaAncora time.Duration
}

// NovaAncoraDeSessaoDeBoot constroi a ancora a partir da primeira mensagem
// observada de uma sessao de boot.
//
// instanteDaAncora e decorridoDaAncora precisam vir da MESMA leitura do relogio,
// tomadas uma imediatamente apos a outra. Leituras de momentos diferentes
// embutiriam na ancora um desvio que depois seria contabilizado como degrau.
func NovaAncoraDeSessaoDeBoot(
	idDoDispositivo identidadededispositivo.IDDoDispositivo,
	idDaSessaoDeBoot identidadededispositivo.IDDaSessaoDeBoot,
	tempoLigadoDaAncora time.Duration,
	instanteDaAncora time.Time,
	decorridoDaAncora time.Duration,
) (AncoraDeSessaoDeBoot, error) {
	if idDoDispositivo.Vazio() {
		return AncoraDeSessaoDeBoot{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoNovaAncora, "ancora exige dispositivo")
	}
	if idDaSessaoDeBoot.Vazio() {
		return AncoraDeSessaoDeBoot{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoNovaAncora, "ancora exige sessao de boot")
	}
	if tempoLigadoDaAncora < 0 {
		return AncoraDeSessaoDeBoot{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoNovaAncora, "tempo ligado de ancoragem negativo")
	}
	if instanteDaAncora.IsZero() {
		return AncoraDeSessaoDeBoot{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoNovaAncora, "ancora exige instante de observacao")
	}
	if decorridoDaAncora < 0 {
		return AncoraDeSessaoDeBoot{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoNovaAncora, "leitura monotonica de ancoragem negativa")
	}
	return AncoraDeSessaoDeBoot{
		idDoDispositivo:     idDoDispositivo,
		idDaSessaoDeBoot:    idDaSessaoDeBoot,
		tempoLigadoDaAncora: tempoLigadoDaAncora,
		instanteDaAncora:    instanteDaAncora.UTC(),
		decorridoDaAncora:   decorridoDaAncora,
	}, nil
}

// IDDoDispositivo devolve o dispositivo ancorado.
func (a AncoraDeSessaoDeBoot) IDDoDispositivo() identidadededispositivo.IDDoDispositivo {
	return a.idDoDispositivo
}

// IDDaSessaoDeBoot devolve a sessao de boot ancorada.
func (a AncoraDeSessaoDeBoot) IDDaSessaoDeBoot() identidadededispositivo.IDDaSessaoDeBoot {
	return a.idDaSessaoDeBoot
}

// TempoLigadoDaAncora devolve o tempo ligado usado como referencia.
func (a AncoraDeSessaoDeBoot) TempoLigadoDaAncora() time.Duration { return a.tempoLigadoDaAncora }

// InstanteDaAncora devolve o instante de parede usado como referencia.
func (a AncoraDeSessaoDeBoot) InstanteDaAncora() time.Time { return a.instanteDaAncora }

// DecorridoDaAncora devolve a leitura monotonica usada como referencia.
func (a AncoraDeSessaoDeBoot) DecorridoDaAncora() time.Duration { return a.decorridoDaAncora }

// EstimarInstanteDaAmostra deriva o instante real estimado em que a amostra foi
// tomada na origem, a partir do seu tempo ligado monotonico.
//
// Erro sistematico conhecido e aceito: o resultado carrega a latencia de
// transporte que existia no instante da ancoragem, alem do desvio do cristal da
// origem acumulado desde entao. Por isso o valor se chama ESTIMADO e nunca
// substitui tempoLigado nem instanteObservado no armazenamento.
//
// Um tempo ligado anterior ao da ancora e recusado: significa que a origem
// reiniciou sem sortear nova sessao de boot, e a serie daquela sessao deixou de
// ser confiavel.
func (a AncoraDeSessaoDeBoot) EstimarInstanteDaAmostra(tempoLigado time.Duration) (time.Time, error) {
	if tempoLigado < a.tempoLigadoDaAncora {
		return time.Time{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoEstimar,
			"tempo ligado anterior a ancora da sessao de boot: origem reiniciou sem renovar a sessao")
	}
	return a.instanteDaAncora.Add(tempoLigado - a.tempoLigadoDaAncora), nil
}

// DesvioDeRelogio devolve o quanto o relogio de parede do gateway divergiu do seu
// proprio monotonico desde a ancoragem.
//
// Positivo significa que a parede andou para a FRENTE alem do tempo que de fato
// passou; negativo, que ela RETROCEDEU. Zero (dentro da granularidade das
// leituras) e a operacao normal.
func (a AncoraDeSessaoDeBoot) DesvioDeRelogio(
	instanteAgora time.Time,
	decorridoAgora time.Duration,
) time.Duration {
	avancoDeParede := instanteAgora.UTC().Sub(a.instanteDaAncora)
	avancoMonotonico := decorridoAgora - a.decorridoDaAncora
	return avancoDeParede - avancoMonotonico
}

// VerificarDegrauDeRelogio recusa a ancora quando o relogio de parede do gateway
// deixou de concordar com a passagem real do tempo.
//
// Esta e a trava que impede o sistema de afirmar um instante que ele nao pode
// mais sustentar. Sem ela, um acerto de hora no meio de uma sessao de boot
// deslocaria em silencio toda a serie derivada dali para frente — e o dado
// resultante seria plausivel, o que e o pior desfecho possivel numa trilha que
// precisa provar QUANDO algo aconteceu.
//
// A falha e CategoriaInterna, e nao EntradaInvalida, de proposito: quem errou foi
// o gateway, nao a origem. O tratamento correto e reancorar a sessao e marcar o
// intervalo como anomalia auditavel — nunca descartar o dado, que continua tendo
// tempo ligado bruto intacto.
func (a AncoraDeSessaoDeBoot) VerificarDegrauDeRelogio(
	instanteAgora time.Time,
	decorridoAgora time.Duration,
	tolerancia time.Duration,
) error {
	desvio := a.DesvioDeRelogio(instanteAgora, decorridoAgora)
	if desvio < 0 {
		desvio = -desvio
	}
	if desvio > tolerancia {
		return falha.Nova(falha.CategoriaInterna, operacaoVerificarDegrau,
			"relogio de parede do gateway divergiu do monotonico: degrau de relogio na sessao de boot")
	}
	return nil
}
