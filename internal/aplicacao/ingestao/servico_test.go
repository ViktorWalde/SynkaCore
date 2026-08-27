package ingestao_test

import (
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/saida/diariosqlite"
	"github.com/ViktorWalde/SynkaCore/internal/aplicacao/ingestao"
	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/relogio"
)

var instanteDeReferencia = time.Date(2026, time.August, 26, 14, 30, 0, 0, time.UTC)

const (
	dispositivoDeTeste = "prensa-01"
	sessaoDeTeste      = "boot-7f3a"
)

func diarioDeTeste(t *testing.T) *diariosqlite.Diario {
	t.Helper()
	diario, err := diariosqlite.Abrir(t.Context(), filepath.Join(t.TempDir(), "diario.db"))
	if err != nil {
		t.Fatalf("abertura do diario falhou: %v", err)
	}
	t.Cleanup(func() { _ = diario.Fechar() })
	return diario
}

func catalogoDeTeste(t *testing.T) *aquisicao.CatalogoDeConteudo {
	t.Helper()
	catalogo, err := aquisicao.NovoCatalogoDeConteudo(aquisicao.TodasAsDefinicoes()...)
	if err != nil {
		t.Fatalf("montagem do catalogo falhou: %v", err)
	}
	return catalogo
}

// envelopeDeTeste constroi um envelope valido com o tempo ligado indicado.
func envelopeDeTeste(t *testing.T, sequencia uint64, tempoLigado time.Duration,
	instanteObservado time.Time) aquisicao.Envelope {
	t.Helper()

	conteudo, err := proto.Marshal(&contratov1.AmostraEscalar{Valor: proto.Float32(65.4)})
	if err != nil {
		t.Fatalf("serializacao falhou: %v", err)
	}

	envelope, err := aquisicao.NovoEnvelope(aquisicao.ParametrosDeEnvelope{
		VersaoDoEsquema:   1,
		IDDoDispositivo:   dispositivoDeTeste,
		IDDaSessaoDeBoot:  sessaoDeTeste,
		NumeroDeSequencia: sequencia,
		//nolint:gosec // tempo ligado de teste, sempre positivo e pequeno
		TempoLigadoMs:     uint64(tempoLigado.Milliseconds()),
		Tipo:              string(aquisicao.TipoAmostraEscalar),
		Conteudo:          conteudo,
		InstanteObservado: instanteObservado,
	}, catalogoDeTeste(t))
	if err != nil {
		t.Fatalf("envelope de teste deveria ser valido: %v", err)
	}
	return envelope
}

func TestIngerirGravaEConfirmaAteASequencia(t *testing.T) {
	diario := diarioDeTeste(t)
	r := relogio.NovoFalso(instanteDeReferencia)
	servico := ingestao.NovoServico(diario, r, "exec-a")

	envelopes := []aquisicao.Envelope{
		envelopeDeTeste(t, 1, time.Second, r.Agora()),
		envelopeDeTeste(t, 2, 2*time.Second, r.Agora()),
		envelopeDeTeste(t, 3, 3*time.Second, r.Agora()),
	}

	confirmacao, err := servico.Ingerir(t.Context(), envelopes, nil)
	if err != nil {
		t.Fatalf("ingestao deveria suceder: %v", err)
	}

	if confirmacao.DuravelAteASequencia != 3 {
		t.Errorf("duravel ate = %d, esperado 3", confirmacao.DuravelAteASequencia)
	}
	if confirmacao.Gravados != 3 {
		t.Errorf("gravados = %d, esperado 3", confirmacao.Gravados)
	}
	if confirmacao.TempoSuspeito {
		t.Error("operacao normal nao deveria marcar o tempo como suspeito")
	}
}

// TestRemessaTotalmenteRejeitadaAindaRecebeResposta cobre um caso que parece de
// borda e nao e.
//
// Sem resposta, a origem retransmitiria para sempre um conteudo que nunca sera
// aceito — e o buffer dela encheria de dado condenado, empurrando dado bom para
// fora.
func TestRemessaTotalmenteRejeitadaAindaRecebeResposta(t *testing.T) {
	servico := ingestao.NovoServico(diarioDeTeste(t), relogio.NovoFalso(instanteDeReferencia), "exec-a")

	confirmacao, err := servico.Ingerir(t.Context(), nil, []uint64{4, 5, 6})
	if err != nil {
		t.Fatalf("remessa toda rejeitada nao e erro: %v", err)
	}
	if len(confirmacao.SequenciasRejeitadas) != 3 {
		t.Errorf("rejeitadas = %v, esperado [4 5 6]", confirmacao.SequenciasRejeitadas)
	}
}

// TestAncoraECriadaNaPrimeiraMensagemEEImutavelDepois protege a estabilidade da
// serie temporal derivada.
//
// Reancorar a cada mensagem faria a latencia de rede VARIAVEL contaminar toda a
// serie, deslocando amostras para frente e para tras sem relacao nenhuma com a
// realidade fisica.
func TestAncoraECriadaNaPrimeiraMensagemEEImutavelDepois(t *testing.T) {
	diario := diarioDeTeste(t)
	r := relogio.NovoFalso(instanteDeReferencia)
	servico := ingestao.NovoServico(diario, r, "exec-a")

	if _, err := servico.Ingerir(t.Context(),
		[]aquisicao.Envelope{envelopeDeTeste(t, 1, 10*time.Second, r.Agora())}, nil); err != nil {
		t.Fatalf("primeira remessa falhou: %v", err)
	}

	dispositivo, _ := identidadededispositivo.AnalisarIDDoDispositivo(dispositivoDeTeste)
	sessao, _ := identidadededispositivo.AnalisarIDDaSessaoDeBoot(sessaoDeTeste)

	primeira, existe, err := diario.LerAncora(t.Context(), dispositivo, sessao, "exec-a")
	if err != nil || !existe {
		t.Fatalf("a ancora deveria existir apos a primeira remessa (err=%v)", err)
	}

	// Tempo passa, a rede fica mais lenta, e uma segunda remessa chega.
	r.Avancar(5 * time.Minute)
	if _, err := servico.Ingerir(t.Context(),
		[]aquisicao.Envelope{envelopeDeTeste(t, 2, 310*time.Second, r.Agora())}, nil); err != nil {
		t.Fatalf("segunda remessa falhou: %v", err)
	}

	segunda, _, err := diario.LerAncora(t.Context(), dispositivo, sessao, "exec-a")
	if err != nil {
		t.Fatalf("leitura da ancora falhou: %v", err)
	}

	if !segunda.Ancora.InstanteDaAncora().Equal(primeira.Ancora.InstanteDaAncora()) {
		t.Errorf("a ancora foi reescrita: %v -> %v",
			primeira.Ancora.InstanteDaAncora(), segunda.Ancora.InstanteDaAncora())
	}
	if segunda.Ancora.TempoLigadoDaAncora() != primeira.Ancora.TempoLigadoDaAncora() {
		t.Error("o tempo ligado de ancoragem foi reescrito")
	}
}

// TestDegrauDeRelogioMarcaOTempoComoSuspeitoSemRecusarODado e a decisao mais sutil
// deste servico.
//
// Quem errou foi o RELOGIO DO GATEWAY, nao a origem. O dado bruto continua valido —
// e o tempo ligado monotonico e a unica coisa que a origem tem autoridade para
// afirmar. Recusar a remessa faria a planta perder dado bom para encobrir um
// defeito nosso.
//
// O que se perde e apenas a confiabilidade do instante DERIVADO, e isso e
// reportado em vez de escondido.
func TestDegrauDeRelogioMarcaOTempoComoSuspeitoSemRecusarODado(t *testing.T) {
	diario := diarioDeTeste(t)
	r := relogio.NovoFalso(instanteDeReferencia)
	servico := ingestao.NovoServico(diario, r, "exec-a")

	if _, err := servico.Ingerir(t.Context(),
		[]aquisicao.Envelope{envelopeDeTeste(t, 1, time.Second, r.Agora())}, nil); err != nil {
		t.Fatalf("primeira remessa falhou: %v", err)
	}

	// Um minuto de operacao normal, e entao alguem acerta a hora para tras.
	r.Avancar(time.Minute)
	r.DarDegrau(-2 * time.Hour)

	confirmacao, err := servico.Ingerir(t.Context(),
		[]aquisicao.Envelope{envelopeDeTeste(t, 2, 61*time.Second, r.Agora())}, nil)
	if err != nil {
		t.Fatalf("o degrau de relogio NAO deveria recusar a remessa: %v", err)
	}

	if !confirmacao.TempoSuspeito {
		t.Error("o degrau de relogio nao foi reportado")
	}
	if confirmacao.DuravelAteASequencia != 2 {
		t.Errorf("duravel ate = %d, esperado 2: o dado precisa ser gravado mesmo assim",
			confirmacao.DuravelAteASequencia)
	}
}

// TestAncoraDeOutraExecucaoNaoProduzDegrauFalso cobre o reinicio do gateway.
//
// A leitura monotonica so tem sentido dentro do processo que a produziu. Sem
// distinguir execucoes, uma ancora gravada antes de um reinicio seria comparada
// com o monotonico da execucao atual — e a diferenca entre duas contagens sem
// relacao apareceria como um degrau de relogio gigante e completamente falso, em
// TODO reinicio.
func TestAncoraDeOutraExecucaoNaoProduzDegrauFalso(t *testing.T) {
	diario := diarioDeTeste(t)

	// Primeira execucao do gateway: cria a ancora depois de horas no ar.
	primeiroRelogio := relogio.NovoFalso(instanteDeReferencia)
	primeiroRelogio.Avancar(6 * time.Hour)
	primeiroServico := ingestao.NovoServico(diario, primeiroRelogio, "exec-a")

	if _, err := primeiroServico.Ingerir(t.Context(),
		[]aquisicao.Envelope{envelopeDeTeste(t, 1, time.Second, primeiroRelogio.Agora())}, nil); err != nil {
		t.Fatalf("primeira execucao falhou: %v", err)
	}

	// O gateway reinicia: o monotonico volta a zero, mas a parede continua andando.
	segundoRelogio := relogio.NovoFalso(primeiroRelogio.Agora())
	segundoRelogio.Avancar(10 * time.Second)
	segundoServico := ingestao.NovoServico(diario, segundoRelogio, "exec-b")

	confirmacao, err := segundoServico.Ingerir(t.Context(),
		[]aquisicao.Envelope{envelopeDeTeste(t, 2, 11*time.Second, segundoRelogio.Agora())}, nil)
	if err != nil {
		t.Fatalf("apos reinicio a ingestao deveria seguir: %v", err)
	}

	if confirmacao.TempoSuspeito {
		t.Error("o reinicio do gateway foi confundido com degrau de relogio: " +
			"a verificacao monotonica nao se aplica entre execucoes distintas")
	}
}

// TestSessoesDeBootDiferentesTemAncorasIndependentes verifica o isolamento.
func TestSessoesDeBootDiferentesTemAncorasIndependentes(t *testing.T) {
	diario := diarioDeTeste(t)
	r := relogio.NovoFalso(instanteDeReferencia)
	servico := ingestao.NovoServico(diario, r, "exec-a")

	if _, err := servico.Ingerir(t.Context(),
		[]aquisicao.Envelope{envelopeDeTeste(t, 1, time.Second, r.Agora())}, nil); err != nil {
		t.Fatalf("primeira sessao falhou: %v", err)
	}

	dispositivo, _ := identidadededispositivo.AnalisarIDDoDispositivo(dispositivoDeTeste)
	outra, _ := identidadededispositivo.AnalisarIDDaSessaoDeBoot("boot-outra")

	if _, existe, err := diario.LerAncora(t.Context(), dispositivo, outra, "exec-a"); err != nil {
		t.Fatalf("leitura falhou: %v", err)
	} else if existe {
		t.Error("uma sessao de boot nova nao deveria herdar a ancora de outra")
	}
}
