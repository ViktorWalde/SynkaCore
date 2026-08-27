// Package simulacao modela um processo industrial real em software.
//
// E o que substitui o hardware fisico. O SynkaCore foi construido software-first:
// a camada de software e validada e estabilizada antes da integracao com
// equipamento, e ate a V1.2 isso significava um simulador ligado direto ao worker.
//
// Na V2.0 o simulador mudou de lugar, e a mudanca importa. Ele nao alimenta mais o
// gateway por chamada de funcao — ele alimenta um NO, que fala com o gateway pelo
// mesmo contrato de fio que um equipamento embarcado usaria. O gateway nao tem como
// saber que do outro lado ha software.
//
// A consequencia pratica: o caminho exercitado em desenvolvimento e o MESMO que
// rodara em producao. Serializacao, lote, contrapressao, retransmissao,
// idempotencia e ancoragem de tempo sao todos exercidos de verdade. Trocar o
// simulador por hardware real deixa de ser uma integracao e passa a ser uma troca
// de quem gera os numeros.
package simulacao

import (
	"math"
	"math/rand/v2"
	"time"
)

// Fase e um trecho do ciclo da camara.
type Fase uint8

const (
	// FaseOciosoInicial: camara em temperatura ambiente, aguardando carga.
	FaseOciosoInicial Fase = iota + 1

	// FaseAquecimento: rampa ate a temperatura de processo.
	FaseAquecimento

	// FaseManutencao: patamar de processo, onde o curtimento acontece.
	FaseManutencao

	// FaseResfriamento: rampa de volta ao ambiente.
	FaseResfriamento

	// FaseOciosoFinal: descarga e espera pelo proximo ciclo.
	FaseOciosoFinal
)

// String devolve o nome estavel da fase, para log e diagnostico.
func (f Fase) String() string {
	switch f {
	case FaseOciosoInicial:
		return "idle_start"
	case FaseAquecimento:
		return "heating"
	case FaseManutencao:
		return "soak"
	case FaseResfriamento:
		return "cooling"
	case FaseOciosoFinal:
		return "idle_end"
	}
	return "unknown"
}

// Parametros do ciclo de uma camara de vacuo de curtimento.
//
// Os limites das fases sao instantes desde o inicio do ciclo, e nao duracoes, para
// que a fase seja resolvida por uma comparacao direta contra o tempo decorrido —
// somar duracoes a cada leitura acumularia erro de arredondamento ao longo de dias
// de operacao continua.
const (
	fimDoOciosoInicial = 30 * time.Second
	fimDoAquecimento   = 60 * time.Second
	fimDaManutencao    = 120 * time.Second
	fimDoResfriamento  = 150 * time.Second
	DuracaoDoCiclo     = 180 * time.Second

	temperaturaAmbiente   = 25.0
	temperaturaDeProcesso = 65.0

	// ruidoDeTemperatura e o desvio padrao do ruido gaussiano, em graus.
	//
	// Ele representa a incerteza do INSTRUMENTO, nao do processo. Sem ruido, a
	// serie sairia perfeitamente lisa — e uma serie lisa demais denuncia
	// simulacao na primeira vez que alguem olha um grafico real ao lado.
	ruidoDeTemperatura = 0.8

	// A pressao cai quando a camara aquece e o vacuo e puxado, e volta ao
	// ambiente na descarga. Em kPa absolutos.
	pressaoAmbiente = 101.3
	pressaoDeVacuo  = 8.0
	ruidoDePressao  = 0.4
)

// Canais que esta camara expoe. Os indices sao o contrato entre o simulador e a
// configuracao de pontos de medicao do gateway.
const (
	CanalDeTemperatura uint32 = 0
	CanalDePressao     uint32 = 1
	CanalDeEstado      uint32 = 2
	CanalDeContagem    uint32 = 3
)

// CamaraDeVacuo simula uma camara de vacuo de curtimento com ciclo realista.
//
// NAO tem estado mutavel proprio alem do gerador: o valor de cada grandeza e uma
// FUNCAO do tempo decorrido no ciclo. Isso torna o simulador reproduzivel e
// permite pular no tempo sem simular os instantes intermediarios — util no teste,
// e impossivel num modelo que integra passo a passo.
type CamaraDeVacuo struct {
	// gerador e injetado para que o teste possa fixar a semente.
	//
	// A V1.x usava ThreadLocalRandom, acessado estaticamente, e registrou a
	// consequencia como debito: nao havia como reproduzir uma execucao nem
	// substituir o gerador em teste sem refatorar a assinatura. Aqui ele entra
	// pelo construtor, e o debito nao chega a existir.
	gerador *rand.Rand
}

// NovaCamaraDeVacuo constroi a camara com o gerador indicado.
//
// Passar nil usa um gerador semeado pelo sistema, que e o comportamento de
// producao. Em teste, injete um gerador com semente fixa.
func NovaCamaraDeVacuo(gerador *rand.Rand) *CamaraDeVacuo {
	if gerador == nil {
		// Gerador nao criptografico de proposito: isto e RUIDO DE INSTRUMENTO numa
		// simulacao de processo, nao material de chave. Usar crypto/rand aqui
		// gastaria entropia do sistema para decidir se a temperatura sai 65,3 ou
		// 65,4 grau. Onde a previsibilidade importa de fato — o identificador de
		// sessao de boot, que compoe a chave de idempotencia — o package
		// identificador usa crypto/rand.
		gerador = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())) //nolint:gosec // ruido de simulacao
	}
	return &CamaraDeVacuo{gerador: gerador}
}

// FaseEm resolve em que fase do ciclo o instante indicado cai.
//
// O tempo decorrido e reduzido ao ciclo com modulo, entao a camara opera
// indefinidamente sem que nada precise ser reiniciado.
func FaseEm(decorrido time.Duration) Fase {
	noCiclo := decorrido % DuracaoDoCiclo
	switch {
	case noCiclo < fimDoOciosoInicial:
		return FaseOciosoInicial
	case noCiclo < fimDoAquecimento:
		return FaseAquecimento
	case noCiclo < fimDaManutencao:
		return FaseManutencao
	case noCiclo < fimDoResfriamento:
		return FaseResfriamento
	}
	return FaseOciosoFinal
}

// CiclosCompletosEm devolve quantos ciclos inteiros terminaram ate o instante.
//
// E a contagem de PECAS produzidas, e por isso ela e ACUMULADA e monotonica: uma
// contagem acumulada perdida no caminho e reposta pela proxima leitura, enquanto
// um incremento perdido some para sempre e o relatorio fica permanentemente errado.
func CiclosCompletosEm(decorrido time.Duration) uint64 {
	if decorrido < 0 {
		return 0
	}
	return uint64(decorrido / DuracaoDoCiclo)
}

// TemperaturaEm devolve a temperatura da camara no instante indicado, em graus Celsius.
//
// Interpolacao linear entre fases, com ruido gaussiano. Um modelo fisico de
// verdade envolveria a lei de resfriamento de Newton e a capacidade termica do
// meio; para o proposito — gerar dado que se comporta como processo industrial —
// a rampa linear e suficiente, e a diferenca so importaria se o objetivo fosse
// validar contra dados reais de equipamento.
func (c *CamaraDeVacuo) TemperaturaEm(decorrido time.Duration) float32 {
	noCiclo := decorrido % DuracaoDoCiclo

	var base float64
	switch FaseEm(decorrido) {
	case FaseOciosoInicial, FaseOciosoFinal:
		base = temperaturaAmbiente
	case FaseAquecimento:
		base = interpolar(noCiclo, fimDoOciosoInicial, fimDoAquecimento,
			temperaturaAmbiente, temperaturaDeProcesso)
	case FaseManutencao:
		base = temperaturaDeProcesso
	case FaseResfriamento:
		base = interpolar(noCiclo, fimDaManutencao, fimDoResfriamento,
			temperaturaDeProcesso, temperaturaAmbiente)
	}

	return float32(base + c.gerador.NormFloat64()*ruidoDeTemperatura)
}

// PressaoEm devolve a pressao absoluta na camara, em kPa.
//
// O vacuo e puxado junto com o aquecimento e liberado no resfriamento, que e o
// comportamento real: a camara nao aquece com a pressao ambiente dentro.
func (c *CamaraDeVacuo) PressaoEm(decorrido time.Duration) float32 {
	noCiclo := decorrido % DuracaoDoCiclo

	var base float64
	switch FaseEm(decorrido) {
	case FaseOciosoInicial, FaseOciosoFinal:
		base = pressaoAmbiente
	case FaseAquecimento:
		base = interpolar(noCiclo, fimDoOciosoInicial, fimDoAquecimento,
			pressaoAmbiente, pressaoDeVacuo)
	case FaseManutencao:
		base = pressaoDeVacuo
	case FaseResfriamento:
		base = interpolar(noCiclo, fimDaManutencao, fimDoResfriamento,
			pressaoDeVacuo, pressaoAmbiente)
	}

	pressao := base + c.gerador.NormFloat64()*ruidoDePressao

	// Pressao absoluta negativa nao existe. O ruido pode empurrar o valor abaixo de
	// zero perto do vacuo, e deixar isso passar produziria dado fisicamente
	// impossivel — que e o tipo de coisa que faz alguem duvidar da serie inteira.
	return float32(math.Max(pressao, 0))
}

// interpolar devolve o valor em `instante` numa rampa linear de `de` ate `ate`.
func interpolar(instante, inicioDaRampa, fimDaRampa time.Duration, de, ate float64) float64 {
	duracao := fimDaRampa - inicioDaRampa
	if duracao <= 0 {
		return ate
	}
	progresso := float64(instante-inicioDaRampa) / float64(duracao)
	return de + (ate-de)*progresso
}
