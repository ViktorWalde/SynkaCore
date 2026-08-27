package apresentacaohttp_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/entrada/apresentacaohttp"
	"github.com/ViktorWalde/SynkaCore/internal/adaptador/entrada/configuracaoarquivo"
	"github.com/ViktorWalde/SynkaCore/internal/adaptador/saida/diariosqlite"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/estadooperacional"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/relogio"
)

var instanteDeReferencia = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

func apresentacaoDeTeste(t *testing.T) *apresentacaohttp.Apresentacao {
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

	r := relogio.NovoFalso(instanteDeReferencia)
	return apresentacaohttp.NovaApresentacao(diario, catalogo,
		estadooperacional.NovoRastreador(r.Agora(), nil), r,
		slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))
}

// descritorDeTeste declara os quatro canais que a camara de vacuo expoe.
func descritorDeTeste() aquisicao.DescritorDaOrigem {
	return aquisicao.DescritorDaOrigem{
		VersaoDoFirmware: "synkacore-no/2.0",
		ModeloDoHardware: "simulacao/camara-de-vacuo",
		Canais: []aquisicao.DescritorDeCanal{
			{Endereco: aquisicao.EnderecoDeCanal{IndiceDoCanal: 0}, Grandeza: 1, Unidade: "Cel"},
			{Endereco: aquisicao.EnderecoDeCanal{IndiceDoCanal: 1}, Grandeza: 2, Unidade: "kPa"},
			{Endereco: aquisicao.EnderecoDeCanal{IndiceDoCanal: 2}, Grandeza: 9, Unidade: "1"},
			{Endereco: aquisicao.EnderecoDeCanal{IndiceDoCanal: 3}, Grandeza: 10, Unidade: "1"},
		},
	}
}

func pedirEsboco(t *testing.T, apresentacao *apresentacaohttp.Apresentacao) string {
	t.Helper()

	servidor := httptest.NewServer(apresentacao.Rotas())
	t.Cleanup(servidor.Close)

	requisicao, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		servidor.URL+apresentacaohttp.CaminhoDoEsboco, nil)
	if err != nil {
		t.Fatalf("montagem da requisicao falhou: %v", err)
	}
	resposta, err := servidor.Client().Do(requisicao)
	if err != nil {
		t.Fatalf("requisicao falhou: %v", err)
	}
	defer func() { _ = resposta.Body.Close() }()

	if resposta.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, esperado 200", resposta.StatusCode)
	}
	corpo, err := io.ReadAll(resposta.Body)
	if err != nil {
		t.Fatalf("leitura falhou: %v", err)
	}
	return string(corpo)
}

// TestOEsbocoGeradoECarregavel e o teste que impede a ferramenta de virar armadilha.
//
// Um gerador que produz configuracao invalida e pior que nenhum gerador: ele custa o
// tempo de quem seguiu a instrucao, e queima a confianca na ferramenta inteira.
//
// O teste faz exatamente o que o esboco pede — substituir os marcadores AJUSTAR — e
// entrega o resultado ao carregador de verdade.
func TestOEsbocoGeradoECarregavel(t *testing.T) {
	apresentacao := apresentacaoDeTeste(t)
	apresentacao.RegistrarDescritor("camara-de-vacuo-01", descritorDeTeste(), instanteDeReferencia)

	esboco := pedirEsboco(t, apresentacao)

	// O tecnico faz o que o arquivo manda.
	preenchido := strings.NewReplacer(
		"AJUSTAR-nome-da-instalacao", "planta-piloto",
		"AJUSTAR-camara-de-vacuo-01-canal-0", "curtimento.camara-01.temperatura",
		"AJUSTAR-camara-de-vacuo-01-canal-1", "curtimento.camara-01.pressao",
		"AJUSTAR-camara-de-vacuo-01-canal-2", "curtimento.camara-01.estado",
		"AJUSTAR-camara-de-vacuo-01-canal-3", "curtimento.camara-01.pecas",
	).Replace(esboco)

	// Conferido apenas nas linhas de CONFIGURACAO, ignorando comentario: o cabecalho
	// do esboco menciona "AJUSTAR" ao explicar o que fazer, e essa mencao deve
	// continuar la depois de o arquivo estar preenchido.
	for _, linha := range strings.Split(preenchido, "\n") {
		semEspaco := strings.TrimSpace(linha)
		if strings.HasPrefix(semEspaco, "#") {
			continue
		}
		if strings.Contains(semEspaco, "AJUSTAR") {
			t.Errorf("sobrou marcador nao preenchido numa linha de configuracao: %q", semEspaco)
		}
	}

	caminho := filepath.Join(t.TempDir(), "instalacao.yaml")
	if err := os.WriteFile(caminho, []byte(preenchido), 0o600); err != nil {
		t.Fatalf("escrita falhou: %v", err)
	}

	configurada, err := configuracaoarquivo.Carregar(caminho)
	if err != nil {
		t.Fatalf("o esboco gerado pelo gateway nao carrega no proprio gateway: %v\n\n%s",
			err, preenchido)
	}
	if canais := configurada.CanaisConfigurados(); len(canais) != 4 {
		t.Errorf("canais configurados = %d, esperado 4", len(canais))
	}
	if configurada.ID() != "planta-piloto" {
		t.Errorf("instalacao = %q", configurada.ID())
	}
}

// TestEsbocoSemOrigemExplicaOMotivo protege quem consulta cedo demais.
//
// Um arquivo em branco pareceria defeito do gateway. O motivo real e quase sempre
// "nenhuma origem se apresentou ainda", que se resolve esperando.
func TestEsbocoSemOrigemExplicaOMotivo(t *testing.T) {
	esboco := pedirEsboco(t, apresentacaoDeTeste(t))

	if !strings.Contains(esboco, "NENHUMA ORIGEM SE APRESENTOU") {
		t.Errorf("o esboco vazio nao explica o motivo:\n%s", esboco)
	}
	if strings.Contains(esboco, "pontos_de_medicao:") {
		t.Error("esboco sem origem nao deveria abrir a lista de pontos")
	}
}

// TestEsbocoPreservaOsPontosJaNomeados impede que regerar apague trabalho feito.
//
// Quem ja configurou metade da planta e regera o esboco para incluir os canais
// novos nao pode perder os nomes que ja escolheu — isso transformaria a ferramenta
// numa armadilha, e ninguem a usaria uma segunda vez.
func TestEsbocoPreservaOsPontosJaNomeados(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "parcial.yaml")
	if err := os.WriteFile(caminho, []byte(`
instalacao: planta-piloto
pontos_de_medicao:
  - dispositivo: camara-de-vacuo-01
    canal: 0
    ponto: curtimento.camara-01.temperatura
    grandeza: temperatura
    unidade: Cel
`), 0o600); err != nil {
		t.Fatalf("escrita falhou: %v", err)
	}
	jaConfigurada, err := configuracaoarquivo.Carregar(caminho)
	if err != nil {
		t.Fatalf("carregamento falhou: %v", err)
	}

	apresentacao := apresentacaoDeTeste(t).ComInstalacao(jaConfigurada)
	apresentacao.RegistrarDescritor("camara-de-vacuo-01", descritorDeTeste(), instanteDeReferencia)

	esboco := pedirEsboco(t, apresentacao)

	if !strings.Contains(esboco, "ponto: curtimento.camara-01.temperatura") {
		t.Errorf("o esboco apagou o nome ja configurado do canal 0:\n%s", esboco)
	}
	// E os canais ainda nao configurados continuam marcados para ajuste.
	if !strings.Contains(esboco, "AJUSTAR-camara-de-vacuo-01-canal-1") {
		t.Errorf("o canal 1, ainda nao configurado, deveria vir marcado:\n%s", esboco)
	}
	if !strings.Contains(esboco, "instalacao: planta-piloto") {
		t.Error("o esboco deveria reaproveitar o nome da instalacao ja configurada")
	}
}

// TestEsbocoTemOrdemEstavel protege a comparacao com o arquivo em uso.
//
// O esboco e regerado e comparado com a configuracao atual. Uma ordem que muda a
// cada consulta — o que um mapa produz naturalmente — tornaria esse diff ilegivel.
func TestEsbocoTemOrdemEstavel(t *testing.T) {
	apresentacao := apresentacaoDeTeste(t)
	apresentacao.RegistrarDescritor("camara-b", descritorDeTeste(), instanteDeReferencia)
	apresentacao.RegistrarDescritor("camara-a", descritorDeTeste(), instanteDeReferencia)

	primeiro := pedirEsboco(t, apresentacao)
	for range 10 {
		if pedirEsboco(t, apresentacao) != primeiro {
			t.Fatal("o esboco variou entre consultas")
		}
	}

	// E os dispositivos saem em ordem alfabetica.
	if strings.Index(primeiro, "dispositivo: camara-a") > strings.Index(primeiro, "dispositivo: camara-b") {
		t.Error("os dispositivos nao saem em ordem estavel")
	}
}
