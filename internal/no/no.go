package no

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/no/simulacao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/relogio"
)

// Configuracao reune os parametros de operacao de uma origem.
//
// Todos sao parametros com padrao dimensionado, nunca constantes embutidas no
// caminho de execucao: uma planta menor roda o mesmo binario com outro arquivo de
// configuracao, e nao com outra compilacao.
type Configuracao struct {
	IDDaInstalacao   string
	IDDoDispositivo  string
	IDDaSessaoDeBoot string

	// IntervaloDeAmostragem e o periodo do laco de leitura.
	IntervaloDeAmostragem time.Duration

	// CapacidadeDoBuffer, em itens, define a autonomia da origem quando o gateway
	// esta fora. Com amostragem a 1 Hz e tres canais, 10.000 itens cobrem cerca de
	// uma hora — folgado para reinicio do gateway, atualizacao e queda curta.
	CapacidadeDoBuffer int

	// EnvelopesPorRemessa e o tamanho do lote.
	//
	// Lote NAO e otimizacao: sem ele, o fsync do gateway sozinho consome toda a
	// capacidade do disco, e uma requisicao por amostra seria inviavel numa origem
	// embarcada.
	EnvelopesPorRemessa int

	// IntervaloDeSaude e o periodo da telemetria interna da origem.
	IntervaloDeSaude time.Duration

	// IntervaloDoDescritor e o periodo da autodeclaracao de canais.
	//
	// Baixa frequencia de proposito: assim a descricao do canal nao precisa viajar
	// em cada amostra, o que economiza a maior parte dos bytes do sistema.
	IntervaloDoDescritor time.Duration

	RecuoBase time.Duration
	RecuoTeto time.Duration
}

// ConfiguracaoPadrao devolve os parametros dimensionados para o cenario alvo.
func ConfiguracaoPadrao() Configuracao {
	return Configuracao{
		IDDaInstalacao:        "planta-piloto",
		IDDoDispositivo:       "camara-de-vacuo-01",
		IntervaloDeAmostragem: time.Second,
		CapacidadeDoBuffer:    10_000,
		EnvelopesPorRemessa:   100,
		IntervaloDeSaude:      30 * time.Second,
		IntervaloDoDescritor:  5 * time.Minute,
		RecuoBase:             time.Second,
		RecuoTeto:             30 * time.Second,
	}
}

// fracaoDeJitterDoRecuo e a amplitude do jitter aplicado ao recuo, em fracao.
const fracaoDeJitterDoRecuo = 0.2

// No e a origem do dado.
type No struct {
	configuracao  Configuracao
	camara        *simulacao.CamaraDeVacuo
	buffer        *Buffer
	transportador Transportador
	relogio       relogio.Relogio
	gerador       *rand.Rand
	registro      *slog.Logger

	// sequencia e o contador dentro da sessao de boot.
	//
	// Reinicia em zero a cada partida, e e por isso que a sessao de boot compoe a
	// chave de idempotencia: sem ela, a mensagem 1 da segunda partida colidiria com
	// a mensagem 1 da primeira e seria descartada como duplicata.
	sequencia uint64

	// estadoDaMaquina e a fase que a origem reportou por ultimo. Serve para emitir
	// mudanca de estado apenas nas TRANSICOES — reportar o estado a cada amostra
	// transformaria um evento discreto em telemetria periodica e destruiria a
	// distincao entre as duas classes.
	faseAnterior simulacao.Fase

	// contagemAnterior evita reemitir a leitura de contador sem que nada tenha sido
	// produzido.
	contagemAnterior uint64

	tentativasSeguidas int
}

// NovoNo constroi a origem.
func NovoNo(configuracao Configuracao, camara *simulacao.CamaraDeVacuo,
	transportador Transportador, r relogio.Relogio, gerador *rand.Rand,
	registro *slog.Logger) *No {

	if gerador == nil {
		// Nao criptografico de proposito: este gerador serve ao JITTER do recuo e ao
		// ruido da simulacao. Jitter previsivel nao e vulnerabilidade — ele existe
		// para dessincronizar a frota, e um atacante que o previsse nao ganharia
		// nada. Ver identificador.Sortear para onde a imprevisibilidade importa.
		gerador = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())) //nolint:gosec // jitter e ruido
	}
	return &No{
		configuracao:  configuracao,
		camara:        camara,
		buffer:        NovoBuffer(configuracao.CapacidadeDoBuffer),
		transportador: transportador,
		relogio:       r,
		gerador:       gerador,
		registro:      registro,
	}
}

// Executar roda a origem ate o contexto ser cancelado.
//
// DOIS LACOS INDEPENDENTES, em goroutines separadas, e a separacao e o ponto
// inteiro do desenho:
//
//	amostragem — periodo fixo, garantido por temporizador. E o que da QUALIDADE ao
//	             dado: uma serie amostrada em intervalos irregulares nao e
//	             comparavel consigo mesma, e nenhuma analise posterior a conserta.
//	despacho   — melhor esforco, sujeito a rede, contrapressao e recuo.
//
// Eles precisam ser goroutines distintas, e nao dois casos de um mesmo select. Num
// select unico, o recuo do despacho — que DORME — bloquearia o temporizador de
// amostragem, e a origem pararia de medir exatamente durante uma queda do gateway.
//
// Isso nao e teoria: a primeira versao deste laco era um select unico, e o teste de
// ponta a ponta flagrou 15 segundos sem amostragem durante uma queda de 12
// segundos. O dado nao se perdeu no caminho — ele NUNCA FOI MEDIDO, que e
// estritamente pior, porque nenhuma retransmissao o traz de volta.
//
// O buffer e protegido por mutex justamente para permitir esta separacao.
func (n *No) Executar(ctx context.Context) error {
	n.emitirDescritor()

	var lacos sync.WaitGroup
	lacos.Add(2)

	go func() {
		defer lacos.Done()
		n.lacoDeAmostragem(ctx)
	}()
	go func() {
		defer lacos.Done()
		n.lacoDeDespacho(ctx)
	}()

	lacos.Wait()

	// Desligamento limpo: tenta entregar o que sobrou no buffer. O contexto do laco
	// ja foi cancelado, entao isto usa um proprio, curto — sem ele, todo
	// desligamento perderia o que estava pronto para ir, e desligamento e rotina:
	// atualizacao, reinicio, manutencao.
	n.despacharNoDesligamento()
	return ctx.Err()
}

// lacoDeAmostragem le a camara em periodo fixo. NADA aqui pode bloquear em rede.
func (n *No) lacoDeAmostragem(ctx context.Context) {
	temporizadorDeAmostragem := time.NewTicker(n.configuracao.IntervaloDeAmostragem)
	defer temporizadorDeAmostragem.Stop()

	temporizadorDeSaude := time.NewTicker(n.configuracao.IntervaloDeSaude)
	defer temporizadorDeSaude.Stop()

	temporizadorDoDescritor := time.NewTicker(n.configuracao.IntervaloDoDescritor)
	defer temporizadorDoDescritor.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-temporizadorDeAmostragem.C:
			n.amostrar()
		case <-temporizadorDeSaude.C:
			n.emitirSaude()
		case <-temporizadorDoDescritor.C:
			n.emitirDescritor()
		}
	}
}

// lacoDeDespacho entrega os lotes ao gateway, e e o unico que pode ficar preso na
// rede.
func (n *No) lacoDeDespacho(ctx context.Context) {
	// A avaliacao roda no orcamento de latencia MAIS APERTADO das classes, que e o
	// do evento discreto. Avaliar no orcamento da amostra faria um alarme esperar a
	// janela da telemetria, e a distincao entre as classes viraria decorativa.
	temporizador := time.NewTicker(aquisicao.ClasseEventoDiscreto.LatenciaMaximaDeEntrega())
	defer temporizador.Stop()

	ultimoDespacho := n.relogio.Agora()

	for {
		select {
		case <-ctx.Done():
			return
		case <-temporizador.C:
			if !n.deveDespachar(ultimoDespacho) {
				continue
			}
			if n.despachar(ctx) {
				ultimoDespacho = n.relogio.Agora()
			}
		}
	}
}

// deveDespachar decide se o lote sai agora.
//
// Tres gatilhos, em ordem de prioridade:
//
//  1. Ha evento discreto pendente — orcamento de 200 ms, sai agora.
//  2. O lote encheu — nao adianta esperar mais, e esperar so aumenta o risco de
//     saturar o buffer.
//  3. O orcamento da amostra venceu — a telemetria nao pode envelhecer no buffer
//     so porque o lote nao encheu.
func (n *No) deveDespachar(ultimoDespacho time.Time) bool {
	ocupacao := n.buffer.Ocupacao()
	if ocupacao == 0 {
		return false
	}
	if n.buffer.TemEventoDiscreto() {
		return true
	}
	if ocupacao >= n.configuracao.EnvelopesPorRemessa {
		return true
	}
	return n.relogio.Agora().Sub(ultimoDespacho) >= aquisicao.ClasseAmostra.LatenciaMaximaDeEntrega()
}

// amostrar le a camara e enfileira o que mudou.
func (n *No) amostrar() {
	decorrido := n.relogio.Decorrido()

	n.enfileirar(&contratov1.Envelope{
		Conteudo: &contratov1.Envelope_AmostraEscalar{
			AmostraEscalar: &contratov1.AmostraEscalar{
				Endereco: endereco(simulacao.CanalDeTemperatura),
				Valor:    proto.Float32(n.camara.TemperaturaEm(decorrido)),
			},
		},
	})

	n.enfileirar(&contratov1.Envelope{
		Conteudo: &contratov1.Envelope_AmostraEscalar{
			AmostraEscalar: &contratov1.AmostraEscalar{
				Endereco: endereco(simulacao.CanalDePressao),
				Valor:    proto.Float32(n.camara.PressaoEm(decorrido)),
			},
		},
	})

	// Mudanca de estado sai apenas na TRANSICAO. Emitir a cada amostra
	// transformaria um evento discreto em telemetria periodica, e a garantia de
	// entrega mais forte passaria a ser paga por dado que nao precisa dela.
	if fase := simulacao.FaseEm(decorrido); fase != n.faseAnterior {
		n.faseAnterior = fase
		n.enfileirar(&contratov1.Envelope{
			Conteudo: &contratov1.Envelope_MudancaDeEstadoDeMaquina{
				MudancaDeEstadoDeMaquina: &contratov1.MudancaDeEstadoDeMaquina{
					Endereco: endereco(simulacao.CanalDeEstado),
					Estado:   estadoDaFase(fase).Enum(),
				},
			},
		})
	}

	// Contagem ACUMULADA, nunca incremental: uma leitura acumulada perdida e
	// reposta pela proxima, enquanto um incremento perdido some para sempre e o
	// relatorio fica permanentemente errado.
	if contagem := simulacao.CiclosCompletosEm(decorrido); contagem != n.contagemAnterior {
		n.contagemAnterior = contagem
		n.enfileirar(&contratov1.Envelope{
			Conteudo: &contratov1.Envelope_LeituraDeContador{
				LeituraDeContador: &contratov1.LeituraDeContador{
					Endereco:          endereco(simulacao.CanalDeContagem),
					ContagemAcumulada: proto.Uint64(contagem),
				},
			},
		})
	}
}

// emitirSaude enfileira a telemetria interna da origem.
//
// Ela carrega tambem o marcador de lacuna, quando houve descarte. A ordem importa:
// a lacuna e enfileirada ANTES da saude, para que o marcador nao seja ele proprio
// vitima da saturacao que esta denunciando.
func (n *No) emitirSaude() {
	if lacuna := n.buffer.TomarLacuna(); !lacuna.Vazia() {
		n.enfileirar(&contratov1.Envelope{
			Conteudo: &contratov1.Envelope_LacunaDeBuffer{
				LacunaDeBuffer: &contratov1.LacunaDeBuffer{
					RegistrosPerdidos:        proto.Uint64(lacuna.Registros),
					PrimeiraSequenciaPerdida: proto.Uint64(lacuna.PrimeiraSequencia),
					UltimaSequenciaPerdida:   proto.Uint64(lacuna.UltimaSequencia),
				},
			},
		})
		n.registro.Warn("buffer saturou e houve descarte",
			slog.Uint64("registros_perdidos", lacuna.Registros))
	}

	// Um processo em software nao tem fragmentacao de heap para reportar, e
	// inventar numeros seria pior que nao mandar nada — telemetria fabricada e
	// exatamente o tipo de coisa que faz alguem confiar num indicador falso. O que
	// esta origem PODE afirmar de verdade e a ocupacao do proprio buffer.
	n.enfileirar(&contratov1.Envelope{
		Conteudo: &contratov1.Envelope_SaudeDaOrigem{
			SaudeDaOrigem: &contratov1.SaudeDaOrigem{
				BytesUsadosNoBuffer: proto.Uint32(uint32(n.buffer.BytesEstimados())),
			},
		},
	})
}

// emitirDescritor enfileira a autodeclaracao de canais.
//
// Serve de rede de protecao de comissionamento: o gateway compara o que a origem
// acredita medir com o mapeamento que ele mesmo tem, e denuncia divergencia. Canal
// trocado no painel deixa de ser erro silencioso.
func (n *No) emitirDescritor() {
	periodo := uint32(n.configuracao.IntervaloDeAmostragem.Milliseconds())

	n.enfileirar(&contratov1.Envelope{
		Conteudo: &contratov1.Envelope_DescritorDaOrigem{
			DescritorDaOrigem: &contratov1.DescritorDaOrigem{
				VersaoDoFirmware: proto.String("synkacore-no/2.0"),
				ModeloDoHardware: proto.String("simulacao/camara-de-vacuo"),
				Canais: []*contratov1.DescritorDeCanal{
					{
						Endereco:              endereco(simulacao.CanalDeTemperatura),
						Grandeza:              contratov1.Grandeza_GRANDEZA_TEMPERATURA.Enum(),
						Unidade:               proto.String("Cel"),
						PeriodoDeAmostragemMs: proto.Uint32(periodo),
					},
					{
						Endereco:              endereco(simulacao.CanalDePressao),
						Grandeza:              contratov1.Grandeza_GRANDEZA_PRESSAO.Enum(),
						Unidade:               proto.String("kPa"),
						PeriodoDeAmostragemMs: proto.Uint32(periodo),
					},
					{
						Endereco: endereco(simulacao.CanalDeEstado),
						Grandeza: contratov1.Grandeza_GRANDEZA_ESTADO_DIGITAL.Enum(),
						Unidade:  proto.String("1"),
					},
					{
						Endereco: endereco(simulacao.CanalDeContagem),
						Grandeza: contratov1.Grandeza_GRANDEZA_CONTAGEM_DE_PECAS.Enum(),
						Unidade:  proto.String("1"),
					},
				},
			},
		},
	})
}

// enfileirar carimba o envelope e o entrega ao buffer.
//
// O chamador monta apenas o CONTEUDO; a sequencia e o tempo ligado sao atribuidos
// AQUI, num lugar so. Atribui-los em cada chamador abriria a porta para dois
// envelopes com o mesmo numero na mesma sessao — e numeros repetidos fariam o
// gateway descartar um deles como duplicata, em silencio.
func (n *No) enfileirar(envelope *contratov1.Envelope) {
	n.sequencia++
	envelope.NumeroDeSequencia = proto.Uint64(n.sequencia)
	envelope.TempoLigadoMs = proto.Uint64(uint64(n.relogio.Decorrido().Milliseconds()))

	n.buffer.Acrescentar(envelope, ClassesDe([]*contratov1.Envelope{envelope})[0])
}

// despachar tenta entregar um lote. Devolve se a entrega foi confirmada.
func (n *No) despachar(ctx context.Context) bool {
	envelopes := n.buffer.Drenar(n.configuracao.EnvelopesPorRemessa)
	if len(envelopes) == 0 {
		return false
	}

	remessa := &contratov1.Remessa{
		VersaoDoEsquema:  proto.Uint32(uint32(aquisicao.VersaoMaximaDoEsquema)),
		IdDaInstalacao:   proto.String(n.configuracao.IDDaInstalacao),
		IdDoDispositivo:  proto.String(n.configuracao.IDDoDispositivo),
		IdDaSessaoDeBoot: proto.String(n.configuracao.IDDaSessaoDeBoot),
		Envelopes:        envelopes,
	}

	confirmacao, err := n.transportador.Despachar(ctx, remessa)
	if err != nil {
		n.tratarFalhaDeDespacho(ctx, envelopes, err)
		return false
	}

	n.tentativasSeguidas = 0
	n.registro.Debug("remessa confirmada",
		slog.Int("envelopes", len(envelopes)),
		slog.Uint64("duravel_ate", confirmacao.GetDuravelAteASequencia()),
		slog.Int("rejeitados", len(confirmacao.GetSequenciasRejeitadasDefinitivamente())))

	if rejeitados := confirmacao.GetSequenciasRejeitadasDefinitivamente(); len(rejeitados) > 0 {
		// Rejeicao definitiva NAO volta para o buffer. Retransmitir conteudo que o
		// gateway nunca vai aceitar faria a origem tentar para sempre e nunca
		// avancar — e o buffer encheria com dado condenado, empurrando dado bom
		// para fora.
		n.registro.Warn("envelopes rejeitados definitivamente pelo gateway e descartados",
			slog.Int("quantidade", len(rejeitados)))
	}
	return true
}

// tratarFalhaDeDespacho decide o destino do lote conforme a natureza da falha.
//
// TRES respostas diferentes, e confundi-las custa caro nos dois sentidos: tratar o
// permanente como retentavel faz a origem tentar para sempre e nunca avancar; tratar
// o transitorio como permanente joga fora dado bom por causa de um problema alheio.
//
//	EntradaInvalida   — a remessa nunca sera aceita. DESCARTA, porque retransmitir
//	                    nao adianta e o buffer encheria de dado condenado,
//	                    empurrando dado bom para fora.
//	PermissaoNegada   — identidade recusada. MANTEM e grita: o dado e bom e volta a
//	                    ser aceito quando alguem corrigir a configuracao, mas o
//	                    problema nao se resolve sozinho.
//	demais            — falha transitoria. MANTEM e recua.
func (n *No) tratarFalhaDeDespacho(ctx context.Context, envelopes []*contratov1.Envelope, err error) {
	switch falha.CategoriaDe(err) {
	// Sem clausula default: o linter exhaustive cobra uma decisao para toda
	// categoria nova. Uma categoria que caisse no padrao herdaria um comportamento
	// que ninguem escolheu para ela — e aqui a escolha errada custa dado.
	case falha.CategoriaEntradaInvalida:
		n.registro.Error("o gateway recusou a remessa definitivamente; os envelopes foram descartados",
			slog.Int("envelopes", len(envelopes)),
			slog.String("erro", err.Error()))

	// Credencial recusada e identidade recusada recebem o MESMO tratamento, e a
	// razao e a mesma nos dois: o dado e bom, quem esta errado e a configuracao, e
	// ela e corrigivel. Descartar perderia dado por um problema que se resolve
	// editando um arquivo ou reemitindo um certificado.
	case falha.CategoriaPermissaoNegada, falha.CategoriaNaoAutenticado:
		// Registrado a CADA tentativa, e nao so nas tres primeiras como as demais
		// falhas. Configuracao errada nao se resolve esperando, e um aviso que some
		// depois de tres linhas seria perdido justamente no caso em que alguem
		// precisa agir.
		n.registro.Error("CREDENCIAL OU IDENTIDADE RECUSADA pelo gateway; o dado permanece no buffer",
			slog.String("id_do_dispositivo", n.configuracao.IDDoDispositivo),
			slog.String("categoria", falha.CategoriaDe(err).String()),
			slog.String("erro", err.Error()))
		n.devolverERecuar(ctx, envelopes, err)

	// Todas transitorias, por motivos diferentes: o gateway caiu, esta saturado, tem
	// um defeito, ou respondeu algo que nao deveria. Em nenhuma delas o dado esta
	// errado — o que falhou foi a entrega, e entrega se tenta de novo.
	//
	// EntregaDuplicada nunca deveria chegar aqui como erro (o gateway a trata como
	// sucesso), e RestritaPorLicenca nunca pode originar do caminho de aquisicao.
	// Ambas caem no comportamento conservador: preservar o dado.
	case falha.CategoriaIndisponivel, falha.CategoriaRecursoEsgotado,
		falha.CategoriaInterna, falha.CategoriaNaoEncontrado,
		falha.CategoriaEntregaDuplicada, falha.CategoriaRestritaPorLicenca:
		n.devolverERecuar(ctx, envelopes, err)
		n.registrarFalhaTransitoria(err)
	}
}

// devolverERecuar recoloca o lote no buffer e espera antes da proxima tentativa.
func (n *No) devolverERecuar(ctx context.Context, envelopes []*contratov1.Envelope, err error) {
	n.buffer.Devolver(envelopes, ClassesDe(envelopes))

	espera := n.esperaAntesDaProximaTentativa(err)
	n.tentativasSeguidas++

	// A espera respeita o cancelamento: um desligamento durante o recuo nao pode
	// ficar preso ate o temporizador vencer.
	temporizador := time.NewTimer(espera)
	defer temporizador.Stop()
	select {
	case <-ctx.Done():
	case <-temporizador.C:
	}
}

// esperaAntesDaProximaTentativa escolhe entre o que o gateway MEDIU e o que a
// origem adivinha.
//
// Recuo exponencial existe porque quem recua nao tem informacao nenhuma: a origem
// nao sabe se o gateway caiu, se a rede sumiu ou se ele esta apenas cheio, entao
// dobra a espera ate acertar. Quando o gateway responde 429 com Retry-After, essa
// ignorancia acaba — ele mediu quanto tempo a fila leva para drenar, e preferir um
// palpite a uma medicao seria trocar informacao por ritual.
//
// DUAS TRAVAS sobre o numero recebido, e as duas importam:
//
//   - O TETO da origem continua valendo. Um gateway defeituoso que pedisse uma hora
//     calaria a frota inteira por uma hora, e a origem so descobriria o engano
//     quando o buffer estourasse. A origem obedece, mas nao ate o ponto de parar de
//     verificar.
//   - O JITTER e da origem, e nao do gateway. O gateway manda o mesmo numero para
//     todas as origens; sem espalhar, elas voltariam juntas e o pico sincronizado
//     derrubaria de vez um gateway que estava apenas saturado.
func (n *No) esperaAntesDaProximaTentativa(err error) time.Duration {
	pedida, gatewayPediu := EsperaSolicitada(err)
	if !gatewayPediu {
		return EsperaDeRecuo(n.tentativasSeguidas, n.configuracao.RecuoBase,
			n.configuracao.RecuoTeto, fracaoDeJitterDoRecuo, n.gerador.Float64)
	}

	if pedida > n.configuracao.RecuoTeto {
		n.registro.Warn("o gateway pediu uma espera acima do teto desta origem; aplicado o teto",
			slog.Duration("pedida", pedida),
			slog.Duration("aplicada", n.configuracao.RecuoTeto))
		pedida = n.configuracao.RecuoTeto
	}
	return ComJitter(pedida, fracaoDeJitterDoRecuo, n.gerador.Float64)
}

// registrarFalhaTransitoria avisa nas primeiras tentativas e cala depois.
//
// Uma queda longa produziria milhares de linhas identicas, e o disco de um
// equipamento que ninguem visita e finito. As primeiras informam; da terceira em
// diante o log so repetiria.
func (n *No) registrarFalhaTransitoria(err error) {
	if n.tentativasSeguidas > 3 {
		return
	}
	n.registro.Warn("despacho falhou; a origem vai retransmitir",
		slog.String("categoria", falha.CategoriaDe(err).String()),
		slog.String("erro", err.Error()),
		slog.Int("bufferizados", n.buffer.Ocupacao()))
}

// tempoLimiteDoDesligamento e quanto a origem espera para entregar o que sobrou.
const tempoLimiteDoDesligamento = 5 * time.Second

// despacharNoDesligamento faz uma ultima tentativa de entregar o buffer.
//
// Usa contexto proprio porque o do laco ja foi cancelado. Sem isto, todo
// desligamento perderia o que estava pronto para ir — e desligamento e rotina:
// atualizacao, reinicio, manutencao.
func (n *No) despacharNoDesligamento() {
	if n.buffer.Ocupacao() == 0 {
		return
	}

	ctx, cancelar := context.WithTimeout(context.Background(), tempoLimiteDoDesligamento)
	defer cancelar()

	pendentes := n.buffer.Ocupacao()
	n.registro.Info("desligando: entregando o que resta no buffer",
		slog.Int("pendentes", pendentes))

	for n.buffer.Ocupacao() > 0 && ctx.Err() == nil {
		if !n.despachar(ctx) {
			break
		}
	}

	if restantes := n.buffer.Ocupacao(); restantes > 0 {
		// Dito em voz alta, e nao engolido: a origem esta desligando com dado que
		// nao coube. Num equipamento real isso estaria em flash e sobreviveria; aqui
		// nao sobrevive, e esconder isso seria mentir sobre o que se perdeu.
		n.registro.Error("desligamento com dado nao entregue",
			slog.Int("registros_nao_entregues", restantes))
	}
}

// endereco monta o endereco de um canal do modulo raiz.
func endereco(canal uint32) *contratov1.EnderecoDeCanal {
	return &contratov1.EnderecoDeCanal{
		IndiceDoModulo: proto.Uint32(0),
		IndiceDoCanal:  proto.Uint32(canal),
	}
}

// estadoDaFase traduz a fase do processo simulado para o estado de maquina do
// contrato.
//
// A camara ociosa e OCIOSA, e nao PARADA: ela esta saudavel, apenas sem carga.
// Somar as duas faria o indicador de disponibilidade culpar a maquina por um
// problema de logistica.
func estadoDaFase(fase simulacao.Fase) contratov1.EstadoDeMaquina {
	switch fase {
	case simulacao.FaseAquecimento, simulacao.FaseManutencao, simulacao.FaseResfriamento:
		return contratov1.EstadoDeMaquina_ESTADO_DE_MAQUINA_RODANDO
	case simulacao.FaseOciosoInicial, simulacao.FaseOciosoFinal:
		return contratov1.EstadoDeMaquina_ESTADO_DE_MAQUINA_OCIOSA
	}
	return contratov1.EstadoDeMaquina_ESTADO_DE_MAQUINA_NAO_ESPECIFICADO
}
