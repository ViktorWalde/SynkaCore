// Package ingestao orquestra o caminho de aquisicao: receber, ancorar o tempo,
// gravar de forma duravel e confirmar.
//
// Este e o caminho que NUNCA pode parar. Toda decisao aqui e tomada com essa
// prioridade: entre perder dado e ficar lento, fica lento; entre perder dado e
// gastar disco, gasta disco; entre perder dado e recusar a remessa, recusa — porque
// remessa recusada e retransmitida, e dado perdido nao volta.
package ingestao

import (
	"context"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/saida/diariosqlite"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/autoridadedetempo"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/relogio"
)

const operacaoIngerir = "ingestao.Ingerir"

// Servico e o caso de uso de ingestao.
type Servico struct {
	diario       *diariosqlite.Diario
	relogio      relogio.Relogio
	idDaExecucao string

	// toleranciaDeDegrau e o quanto o relogio de parede pode divergir do
	// monotonico antes de a sessao ser marcada como temporalmente suspeita.
	toleranciaDeDegrau time.Duration
}

// NovoServico constroi o caso de uso.
//
// idDaExecucao identifica esta partida do processo. Ele existe porque a leitura
// monotonica so tem sentido dentro do processo que a produziu: sem distinguir
// execucoes, uma ancora gravada antes de um reinicio seria comparada com o
// monotonico da execucao atual, e a diferenca entre duas contagens sem relacao
// apareceria como um degrau de relogio enorme e completamente falso.
func NovoServico(diario *diariosqlite.Diario, r relogio.Relogio, idDaExecucao string) *Servico {
	return &Servico{
		diario:             diario,
		relogio:            r,
		idDaExecucao:       idDaExecucao,
		toleranciaDeDegrau: autoridadedetempo.ToleranciaDeDegrauPadrao,
	}
}

// Confirmacao e o que o gateway devolve a origem apos uma remessa.
type Confirmacao struct {
	// DuravelAteASequencia e ate onde a origem pode liberar o buffer.
	DuravelAteASequencia uint64

	// SequenciasRejeitadas foram recusadas por conteudo invalido. Retransmitir NAO
	// adianta, e a origem deve descarta-las em vez de tentar para sempre.
	SequenciasRejeitadas []uint64

	// Gravados e Duplicados existem para observabilidade. Duplicacao subindo
	// indica que as confirmacoes nao estao chegando de volta a origem — uma falha
	// de rede assimetrica que, sem esse numero, so apareceria como disco enchendo.
	Gravados   int
	Duplicados int

	// TempoSuspeito informa que a serie derivada desta sessao nao pode ser tratada
	// como temporalmente confiavel ate a sessao ser reancorada.
	//
	// A remessa NAO e recusada por isso: o dado bruto continua valido e e a unica
	// coisa que a origem tem autoridade para afirmar. Quem errou foi o relogio do
	// gateway, e descartar dado bom para encobrir defeito nosso seria o pior
	// desfecho possivel.
	TempoSuspeito bool
}

// Ingerir grava uma remessa decodificada e devolve a confirmacao.
//
// A ORDEM DAS ETAPAS E A GARANTIA. A confirmacao so e emitida depois de o diario
// ter confirmado a transacao. Confirmar antes de gravar faria a origem liberar o
// buffer de dado que nao existe em lugar nenhum — que e precisamente o modo de
// falha que este sistema inteiro existe para tornar impossivel.
func (s *Servico) Ingerir(ctx context.Context, envelopes []aquisicao.Envelope,
	sequenciasRejeitadas []uint64) (Confirmacao, error) {

	if len(envelopes) == 0 {
		// Remessa inteiramente rejeitada ainda merece resposta: sem ela a origem
		// retransmitiria para sempre um conteudo que nunca sera aceito.
		return Confirmacao{SequenciasRejeitadas: sequenciasRejeitadas}, nil
	}

	tempoSuspeito, err := s.ancorarSessao(ctx, envelopes[0])
	if err != nil {
		return Confirmacao{}, err
	}

	resultado, err := s.diario.GravarLote(ctx, envelopes)
	if err != nil {
		return Confirmacao{}, err
	}

	return Confirmacao{
		DuravelAteASequencia: resultado.MaiorSequenciaDuravel,
		SequenciasRejeitadas: sequenciasRejeitadas,
		Gravados:             resultado.Gravados,
		Duplicados:           resultado.Duplicados,
		TempoSuspeito:        tempoSuspeito,
	}, nil
}

// ancorarSessao garante que a sessao de boot tenha ancora, e verifica o relogio.
//
// Devolve se o tempo derivado desta sessao deve ser considerado suspeito.
func (s *Servico) ancorarSessao(ctx context.Context, primeiro aquisicao.Envelope) (bool, error) {
	dispositivo := primeiro.IDDoDispositivo()
	sessao := primeiro.IDDaSessaoDeBoot()

	persistida, existe, err := s.diario.LerAncora(ctx, dispositivo, sessao, s.idDaExecucao)
	if err != nil {
		return false, err
	}

	if !existe {
		// As duas leituras precisam vir do MESMO ponto no tempo, uma imediatamente
		// apos a outra. Tomadas de momentos distintos, embutiriam na ancora um
		// desvio que depois seria contabilizado como degrau.
		agora := s.relogio.Agora()
		decorrido := s.relogio.Decorrido()

		ancora, err := autoridadedetempo.NovaAncoraDeSessaoDeBoot(
			dispositivo, sessao, primeiro.TempoLigado(), agora, decorrido)
		if err != nil {
			return false, falha.Envolver(falha.CategoriaInterna, operacaoIngerir,
				"nao foi possivel ancorar a sessao de boot", err)
		}
		if err := s.diario.GravarAncora(ctx, ancora, s.idDaExecucao, agora); err != nil {
			return false, err
		}
		return false, nil
	}

	// A verificacao de degrau so e possivel dentro da execucao que criou a ancora:
	// leituras monotonicas de processos diferentes nao sao comparaveis. Apos um
	// reinicio do gateway, a ancora continua valendo para ESTIMAR — a parede
	// gravada nao mudou —, mas a deteccao de degrau fica indisponivel ate a sessao
	// ser reancorada. Dizer isso e melhor que fingir uma verificacao que nao
	// aconteceu.
	if !persistida.DaExecucaoAtual {
		return false, nil
	}

	err = persistida.Ancora.VerificarDegrauDeRelogio(
		s.relogio.Agora(), s.relogio.Decorrido(), s.toleranciaDeDegrau)
	return err != nil, nil
}
