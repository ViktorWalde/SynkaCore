package no_test

import (
	"context"
	"io"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
	"time"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/no"
	"github.com/ViktorWalde/SynkaCore/internal/no/simulacao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/relogio"
)

// transportadorControlado permite derrubar e religar o gateway em teste.
//
// E a razao de Transportador ser interface. Os tres cenarios que decidem se este
// componente funciona — gateway fora, contrapressao e retomada — sao os mais caros
// de reproduzir com rede de verdade, e sem um duble ficariam sem exercicio.
type transportadorControlado struct {
	mutex      sync.Mutex
	disponivel bool
	recebidos  []uint64

	// tempoLigadoDasAmostras guarda o instante MONOTONICO de cada amostra do canal
	// de temperatura, na ordem de sequencia.
	//
	// E este campo, e nao a lista de sequencias, que detecta parada de amostragem.
	// Numero de sequencia e atribuido no enfileiramento, entao um amostrador
	// travado produz MENOS amostras, todas perfeitamente contiguas — a
	// contiguidade e cega para o defeito. O espacamento entre tempos ligados nao e.
	tempoLigadoDasAmostras []uint64
}

func novoTransportadorControlado() *transportadorControlado {
	return &transportadorControlado{disponivel: true}
}

func (t *transportadorControlado) Despachar(_ context.Context,
	remessa *contratov1.Remessa) (*contratov1.ConfirmacaoDeRemessa, error) {

	t.mutex.Lock()
	defer t.mutex.Unlock()

	if !t.disponivel {
		return nil, falha.Nova(falha.CategoriaIndisponivel, "teste", "gateway inalcancavel")
	}

	var maior uint64
	for _, envelope := range remessa.GetEnvelopes() {
		sequencia := envelope.GetNumeroDeSequencia()
		t.recebidos = append(t.recebidos, sequencia)
		if sequencia > maior {
			maior = sequencia
		}
		if amostra := envelope.GetAmostraEscalar(); amostra != nil &&
			amostra.GetEndereco().GetIndiceDoCanal() == simulacao.CanalDeTemperatura {
			t.tempoLigadoDasAmostras = append(t.tempoLigadoDasAmostras, envelope.GetTempoLigadoMs())
		}
	}
	return &contratov1.ConfirmacaoDeRemessa{DuravelAteASequencia: &maior}, nil
}

func (t *transportadorControlado) definirDisponibilidade(disponivel bool) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.disponivel = disponivel
}

func (t *transportadorControlado) sequenciasRecebidas() []uint64 {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return append([]uint64(nil), t.recebidos...)
}

// maiorIntervaloEntreAmostras devolve o maior espacamento observado entre duas
// amostras consecutivas do canal de temperatura, em milissegundos.
func (t *transportadorControlado) maiorIntervaloEntreAmostras() uint64 {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	tempos := append([]uint64(nil), t.tempoLigadoDasAmostras...)
	slices.Sort(tempos)

	var maior uint64
	for indice := 1; indice < len(tempos); indice++ {
		if intervalo := tempos[indice] - tempos[indice-1]; intervalo > maior {
			maior = intervalo
		}
	}
	return maior
}

func registroSilencioso() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func configuracaoDeTeste() no.Configuracao {
	configuracao := no.ConfiguracaoPadrao()
	configuracao.IntervaloDeAmostragem = 10 * time.Millisecond
	configuracao.IntervaloDeSaude = time.Hour
	configuracao.IntervaloDoDescritor = time.Hour
	configuracao.CapacidadeDoBuffer = 100_000
	configuracao.EnvelopesPorRemessa = 50
	configuracao.RecuoBase = 20 * time.Millisecond
	configuracao.RecuoTeto = 80 * time.Millisecond
	return configuracao
}

// TestAmostragemNaoParaQuandoOGatewayCai e o teste mais importante deste package,
// e existe porque a primeira versao do laco ERRAVA exatamente aqui.
//
// Naquela versao, amostragem e despacho compartilhavam um unico select. O recuo do
// despacho DORME, e dormindo bloqueava o temporizador de amostragem: um teste de
// ponta a ponta flagrou 15 segundos sem nenhuma medicao durante uma queda de 12
// segundos do gateway.
//
// A distincao que isso revela e a que da nome ao teste. O buffer protege contra
// perder dado NO CAMINHO — e para isso ele funcionava. Mas dado que nunca foi
// MEDIDO nao esta em buffer nenhum, e nenhuma retransmissao o traz de volta. Uma
// queda de rede havia se transformado num buraco permanente na serie.
//
// A amostragem tem periodo fixo garantido por temporizador, e e isso que da
// qualidade ao dado. Ela nao pode depender da rede — nunca.
func TestAmostragemNaoParaQuandoOGatewayCai(t *testing.T) {
	configuracao := configuracaoDeTeste()
	transportador := novoTransportadorControlado()

	origem := no.NovoNo(configuracao, simulacao.NovaCamaraDeVacuo(rand.New(rand.NewPCG(1, 2))),
		transportador, relogio.Sistema(), rand.New(rand.NewPCG(3, 4)), registroSilencioso())

	ctx, cancelar := context.WithCancel(t.Context())
	concluido := make(chan struct{})
	go func() {
		defer close(concluido)
		_ = origem.Executar(ctx)
	}()

	// Opera normalmente, cai, e volta. Cada trecho dura o mesmo tempo para que a
	// contagem de amostras dos tres seja comparavel.
	const duracaoDeCadaTrecho = 300 * time.Millisecond

	time.Sleep(duracaoDeCadaTrecho)
	transportador.definirDisponibilidade(false)

	time.Sleep(duracaoDeCadaTrecho)
	transportador.definirDisponibilidade(true)

	time.Sleep(duracaoDeCadaTrecho)
	cancelar()
	<-concluido

	recebidas := transportador.sequenciasRecebidas()
	if len(recebidas) == 0 {
		t.Fatal("nenhuma sequencia chegou ao gateway")
	}

	// A VERIFICACAO DECISIVA e o espacamento entre amostras, e nao a contiguidade
	// das sequencias.
	//
	// Isto merece registro porque a primeira versao deste teste checava
	// contiguidade e NAO PEGAVA o defeito. O numero de sequencia e atribuido no
	// enfileiramento: um amostrador travado produz menos amostras, todas
	// perfeitamente contiguas. A contiguidade e cega para uma parada de amostragem
	// — ela so detecta perda no caminho, que e outro problema.
	//
	// O tempo ligado, ao contrario, vem do relogio monotonico no instante da
	// medicao. Um buraco nele significa exatamente o que precisamos recusar: a
	// medicao nao aconteceu.
	maiorIntervalo := transportador.maiorIntervaloEntreAmostras()
	nominal := uint64(configuracao.IntervaloDeAmostragem.Milliseconds())

	// Tres periodos de folga absorvem o agendamento do temporizador sob -race sem
	// deixar passar a parada, que na versao defeituosa chegava ao teto do recuo —
	// varias vezes o periodo de amostragem.
	if limite := nominal * 3; maiorIntervalo > limite {
		t.Errorf("maior intervalo entre amostras = %dms, acima do limite de %dms (nominal %dms): a amostragem parou durante a queda do gateway",
			maiorIntervalo, limite, nominal)
	}

	// A serie tambem precisa ser contigua em sequencia — isto cobre o outro
	// problema, que e o buffer perder o que estava guardando.
	vistas := make(map[uint64]bool, len(recebidas))
	var maior uint64
	for _, sequencia := range recebidas {
		vistas[sequencia] = true
		if sequencia > maior {
			maior = sequencia
		}
	}
	for sequencia := uint64(1); sequencia <= maior; sequencia++ {
		if !vistas[sequencia] {
			t.Fatalf("sequencia %d faltando: o buffer perdeu dado durante a queda (recebidas ate %d)",
				sequencia, maior)
		}
	}
}

// TestReentregaAposQuedaNaoPerdeNemInverteAOrdem verifica que o buffer devolve o
// lote no inicio, e nao no fim.
//
// A confirmacao do gateway e uma FAIXA CONTIGUA — "duravel ate a sequencia N" —, o
// que so e expressavel se os numeros chegarem em ordem. Devolver ao fim do buffer
// faria o lote seguinte sair fora de ordem e a garantia deixaria de ser formulavel.
func TestReentregaAposQuedaNaoPerdeNemInverteAOrdem(t *testing.T) {
	configuracao := configuracaoDeTeste()
	transportador := novoTransportadorControlado()
	transportador.definirDisponibilidade(false)

	origem := no.NovoNo(configuracao, simulacao.NovaCamaraDeVacuo(rand.New(rand.NewPCG(1, 2))),
		transportador, relogio.Sistema(), rand.New(rand.NewPCG(3, 4)), registroSilencioso())

	ctx, cancelar := context.WithCancel(t.Context())
	concluido := make(chan struct{})
	go func() {
		defer close(concluido)
		_ = origem.Executar(ctx)
	}()

	// Acumula com o gateway fora desde a partida.
	time.Sleep(400 * time.Millisecond)
	if recebidas := transportador.sequenciasRecebidas(); len(recebidas) != 0 {
		t.Fatalf("gateway indisponivel recebeu %d sequencias", len(recebidas))
	}

	transportador.definirDisponibilidade(true)
	time.Sleep(500 * time.Millisecond)

	cancelar()
	<-concluido

	recebidas := transportador.sequenciasRecebidas()
	if len(recebidas) == 0 {
		t.Fatal("nada foi entregue apos o gateway voltar")
	}

	for indice := 1; indice < len(recebidas); indice++ {
		if recebidas[indice] <= recebidas[indice-1] {
			t.Fatalf("ordem quebrada na posicao %d: %d apos %d",
				indice, recebidas[indice], recebidas[indice-1])
		}
	}
	if recebidas[0] != 1 {
		t.Errorf("a primeira sequencia entregue foi %d, esperado 1: o inicio do buffer se perdeu",
			recebidas[0])
	}
}
