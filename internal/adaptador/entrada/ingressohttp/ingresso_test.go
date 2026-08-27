package ingressohttp_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/entrada/ingressohttp"
	"github.com/ViktorWalde/SynkaCore/internal/adaptador/saida/diariosqlite"
	"github.com/ViktorWalde/SynkaCore/internal/aplicacao/ingestao"
	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/relogio"
)

var instanteDeReferencia = time.Date(2026, time.August, 26, 14, 30, 0, 0, time.UTC)

// servidorDeTeste monta o caminho de aquisicao inteiro sobre um diario real.
//
// Sem dubles: o valor deste teste esta em exercitar HTTP, codec, servico e diario
// juntos, que e onde os defeitos de fronteira aparecem. Um duble no lugar do
// diario testaria a nossa ideia dele em vez dele.
func servidorDeTeste(t *testing.T) (*httptest.Server, *diariosqlite.Diario) {
	t.Helper()

	diario, err := diariosqlite.Abrir(t.Context(), filepath.Join(t.TempDir(), "diario.db"))
	if err != nil {
		t.Fatalf("abertura do diario falhou: %v", err)
	}
	t.Cleanup(func() { _ = diario.Fechar() })

	catalogo, err := aquisicao.NovoCatalogoDeConteudo(aquisicao.TodasAsDefinicoes()...)
	if err != nil {
		t.Fatalf("montagem do catalogo falhou: %v", err)
	}

	registro := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	r := relogio.NovoFalso(instanteDeReferencia)
	servico := ingestao.NovoServico(diario, r, "exec-teste")

	servidor := httptest.NewServer(ingressohttp.NovoIngresso(servico, catalogo, r, registro).Rotas())
	t.Cleanup(servidor.Close)
	return servidor, diario
}

func remessaSerializada(t *testing.T, envelopes ...*contratov1.Envelope) []byte {
	t.Helper()
	bytes, err := proto.Marshal(&contratov1.Remessa{
		VersaoDoEsquema:  proto.Uint32(1),
		IdDaInstalacao:   proto.String("planta-piloto"),
		IdDoDispositivo:  proto.String("prensa-01"),
		IdDaSessaoDeBoot: proto.String("boot-7f3a"),
		Envelopes:        envelopes,
	})
	if err != nil {
		t.Fatalf("serializacao falhou: %v", err)
	}
	return bytes
}

func envelopeDeAmostra(sequencia uint64) *contratov1.Envelope {
	return &contratov1.Envelope{
		NumeroDeSequencia: proto.Uint64(sequencia),
		TempoLigadoMs:     proto.Uint64(sequencia * 1000),
		Conteudo: &contratov1.Envelope_AmostraEscalar{
			AmostraEscalar: &contratov1.AmostraEscalar{Valor: proto.Float32(65.4)},
		},
	}
}

// respostaDoIngresso e a resposta ja LIDA E FECHADA.
//
// O helper devolve isto em vez de um *http.Response por uma razao concreta: uma
// resposta que escapa da funcao deixa o fechamento a cargo de quem chama, e o
// linter bodyclose acusa — com razao, porque adiar o fechamento para um
// t.Cleanup funciona mas nao e verificavel. Ler e fechar no mesmo lugar remove a
// duvida em vez de suprimi-la.
type respostaDoIngresso struct {
	Status         int
	TipoDeConteudo string
	Corpo          []byte
}

func enviar(t *testing.T, servidor *httptest.Server, corpo []byte) respostaDoIngresso {
	t.Helper()

	requisicao, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		servidor.URL+ingressohttp.CaminhoDeIngestao, bytes.NewReader(corpo))
	if err != nil {
		t.Fatalf("montagem da requisicao falhou: %v", err)
	}
	requisicao.Header.Set("Content-Type", ingressohttp.TipoDeConteudoProtobuf)

	resposta, err := servidor.Client().Do(requisicao)
	if err != nil {
		t.Fatalf("requisicao falhou: %v", err)
	}
	defer func() { _ = resposta.Body.Close() }()

	lido, err := io.ReadAll(resposta.Body)
	if err != nil {
		t.Fatalf("leitura da resposta falhou: %v", err)
	}
	return respostaDoIngresso{
		Status:         resposta.StatusCode,
		TipoDeConteudo: resposta.Header.Get("Content-Type"),
		Corpo:          lido,
	}
}

func lerConfirmacao(t *testing.T, resposta respostaDoIngresso) *contratov1.ConfirmacaoDeRemessa {
	t.Helper()

	var confirmacao contratov1.ConfirmacaoDeRemessa
	if err := proto.Unmarshal(resposta.Corpo, &confirmacao); err != nil {
		t.Fatalf("confirmacao ilegivel: %v", err)
	}
	return &confirmacao
}

func TestRemessaValidaEGravadaEConfirmada(t *testing.T) {
	servidor, diario := servidorDeTeste(t)

	resposta := enviar(t, servidor, remessaSerializada(t,
		envelopeDeAmostra(1), envelopeDeAmostra(2), envelopeDeAmostra(3)))

	if resposta.Status != http.StatusOK {
		t.Fatalf("status = %d, esperado 200", resposta.Status)
	}
	if tipo := resposta.TipoDeConteudo; tipo != ingressohttp.TipoDeConteudoProtobuf {
		t.Errorf("Content-Type = %q, esperado %q", tipo, ingressohttp.TipoDeConteudoProtobuf)
	}

	if confirmacao := lerConfirmacao(t, resposta); confirmacao.GetDuravelAteASequencia() != 3 {
		t.Errorf("duravel ate = %d, esperado 3", confirmacao.GetDuravelAteASequencia())
	}

	registros, err := diario.LerAPartirDe(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("leitura do diario falhou: %v", err)
	}
	if len(registros) != 3 {
		t.Errorf("registros no diario = %d, esperado 3", len(registros))
	}
}

// TestReentregaRespondeSucesso protege o nó de retransmitir para sempre.
//
// Reentrega e consequencia ESPERADA de entrega ao-menos-uma-vez. Se ela fosse
// relatada como falha, a origem retransmitiria o que ja esta salvo, indefinidamente.
func TestReentregaRespondeSucesso(t *testing.T) {
	servidor, diario := servidorDeTeste(t)
	corpo := remessaSerializada(t, envelopeDeAmostra(1), envelopeDeAmostra(2))

	enviar(t, servidor, corpo)
	resposta := enviar(t, servidor, corpo)

	if resposta.Status != http.StatusOK {
		t.Fatalf("status na reentrega = %d, esperado 200", resposta.Status)
	}
	if confirmacao := lerConfirmacao(t, resposta); confirmacao.GetDuravelAteASequencia() != 2 {
		t.Errorf("duravel ate = %d na reentrega, esperado 2",
			confirmacao.GetDuravelAteASequencia())
	}

	registros, err := diario.LerAPartirDe(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("leitura falhou: %v", err)
	}
	if len(registros) != 2 {
		t.Errorf("registros = %d apos reentrega, esperado 2", len(registros))
	}
}

// TestEnvelopeInvalidoVoltaComoRejeicaoDefinitiva verifica a mensagem que faz a
// origem DESCARTAR em vez de tentar para sempre.
func TestEnvelopeInvalidoVoltaComoRejeicaoDefinitiva(t *testing.T) {
	servidor, _ := servidorDeTeste(t)

	// Estado NAO_ESPECIFICADO e recusado: aceita-lo registraria "a maquina esta em
	// algum estado" como se fosse um fato.
	invalido := &contratov1.Envelope{
		NumeroDeSequencia: proto.Uint64(2),
		Conteudo: &contratov1.Envelope_MudancaDeEstadoDeMaquina{
			MudancaDeEstadoDeMaquina: &contratov1.MudancaDeEstadoDeMaquina{}},
	}

	resposta := enviar(t, servidor, remessaSerializada(t,
		envelopeDeAmostra(1), invalido, envelopeDeAmostra(3)))

	if resposta.Status != http.StatusOK {
		t.Fatalf("status = %d: um envelope invalido nao pode derrubar a remessa", resposta.Status)
	}

	confirmacao := lerConfirmacao(t, resposta)
	if confirmacao.GetDuravelAteASequencia() != 3 {
		t.Errorf("duravel ate = %d, esperado 3", confirmacao.GetDuravelAteASequencia())
	}
	rejeitadas := confirmacao.GetSequenciasRejeitadasDefinitivamente()
	if len(rejeitadas) != 1 || rejeitadas[0] != 2 {
		t.Errorf("rejeitadas = %v, esperado [2]", rejeitadas)
	}
}

// TestCorpoInvalidoRespondeDescartar verifica o lado 4xx do mapeamento.
//
// A distincao entre 4xx e 5xx e o que a origem usa para decidir entre DESCARTAR e
// RETRANSMITIR, e errar nos dois sentidos e caro: tratar 4xx como retentavel faz a
// origem tentar para sempre; tratar 5xx como definitivo faz ela jogar fora dado
// bom por causa de um problema alheio.
func TestCorpoInvalidoRespondeDescartar(t *testing.T) {
	servidor, _ := servidorDeTeste(t)

	casos := map[string][]byte{
		"bytes que nao sao protobuf": {0xff, 0xff, 0xff, 0xff, 0xff},
		"remessa sem envelopes":      remessaSerializada(t),
		"corpo vazio":                {},
	}

	for nome, corpo := range casos {
		t.Run(nome, func(t *testing.T) {
			resposta := enviar(t, servidor, corpo)
			if resposta.Status != http.StatusBadRequest {
				t.Errorf("status = %d, esperado 400", resposta.Status)
			}
		})
	}
}

// TestCorpoAcimaDoLimiteERecusado fecha o vetor de exaustao de memoria.
//
// O limite e imposto ANTES da leitura, e nao verificado depois: verificar depois
// exigiria ja ter lido o corpo inteiro, que e exatamente o que o limite existe
// para impedir.
func TestCorpoAcimaDoLimiteERecusado(t *testing.T) {
	servidor, _ := servidorDeTeste(t)

	gigante := make([]byte, ingressohttp.TamanhoMaximoDoCorpo+1024)
	resposta := enviar(t, servidor, gigante)

	if resposta.Status != http.StatusBadRequest {
		t.Errorf("status = %d, esperado 400", resposta.Status)
	}
}

// TestRespostaDeFalhaNaoVazaDetalheInterno verifica a superficie exposta.
//
// A mensagem interna do erro vai para o LOG, onde o operador a le. Devolve-la a
// rede daria a um atacante um mapa do que o gateway valida e como.
func TestRespostaDeFalhaNaoVazaDetalheInterno(t *testing.T) {
	servidor, _ := servidorDeTeste(t)

	resposta := enviar(t, servidor, []byte{0xff, 0xff, 0xff})

	// O corpo carrega apenas o nome da categoria — nada de nome de funcao,
	// caminho de arquivo ou mensagem de validacao.
	if texto := string(resposta.Corpo); texto != "invalid_input" {
		t.Errorf("corpo da falha = %q, esperado apenas a categoria", texto)
	}
}

// TestApenasPostEAceitoNoCaminhoDeIngestao verifica que o caminho de aquisicao nao
// responde a mais nada.
func TestApenasPostEAceitoNoCaminhoDeIngestao(t *testing.T) {
	servidor, _ := servidorDeTeste(t)

	for _, metodo := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(metodo, func(t *testing.T) {
			requisicao, err := http.NewRequestWithContext(t.Context(), metodo,
				servidor.URL+ingressohttp.CaminhoDeIngestao, nil)
			if err != nil {
				t.Fatalf("montagem falhou: %v", err)
			}
			resposta, err := servidor.Client().Do(requisicao)
			if err != nil {
				t.Fatalf("requisicao falhou: %v", err)
			}
			defer func() { _ = resposta.Body.Close() }()

			if resposta.StatusCode == http.StatusOK {
				t.Errorf("%s foi aceito no caminho de aquisicao", metodo)
			}
		})
	}
}
