package configuracaoarquivo_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/entrada/configuracaoarquivo"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/instalacao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

// caminhoDoExemplo aponta para o arquivo que o projeto distribui.
const caminhoDoExemplo = "../../../../configuracao/instalacao.exemplo.yaml"

// TestOExemploDistribuidoCarrega e o teste mais valioso deste package.
//
// O arquivo de exemplo e o primeiro contato de quem vai comissionar uma planta. Um
// exemplo que nao carrega e pior que nenhum exemplo: ele ensina errado e queima a
// confianca de quem seguiu a documentacao.
//
// Como ele e verificado a cada build, o exemplo nao pode envelhecer em silencio
// quando o formato mudar.
func TestOExemploDistribuidoCarrega(t *testing.T) {
	configurada, err := configuracaoarquivo.Carregar(caminhoDoExemplo)
	if err != nil {
		t.Fatalf("o exemplo distribuido nao carrega: %v", err)
	}

	if configurada.ID() != "planta-piloto" {
		t.Errorf("instalacao = %q", configurada.ID())
	}
	if canais := configurada.CanaisConfigurados(); len(canais) != 4 {
		t.Errorf("canais configurados = %d, esperado 4", len(canais))
	}
	if versao := configurada.VersaoDoCatalogoDeMotivos(); versao != 1 {
		t.Errorf("versao do catalogo = %d, esperado 1", versao)
	}

	dispositivo, err := identidadededispositivo.AnalisarIDDoDispositivo("camara-de-vacuo-01")
	if err != nil {
		t.Fatalf("dispositivo invalido: %v", err)
	}
	ponto, existe := configurada.Resolver(instalacao.ChaveDeCanal{
		Dispositivo: dispositivo,
		Endereco:    aquisicao.EnderecoDeCanal{IndiceDoCanal: 0},
	}, time.Now())
	if !existe {
		t.Fatal("o canal 0 do exemplo deveria resolver")
	}
	if ponto.Ponto.String() != "curtimento.camara-01.temperatura" {
		t.Errorf("ponto = %q", ponto.Ponto)
	}
	if ponto.Unidade != "Cel" {
		t.Errorf("unidade = %q", ponto.Unidade)
	}
	if ponto.FaixaMaxima == nil || *ponto.FaixaMaxima != 200 {
		t.Error("a faixa do exemplo nao foi carregada")
	}
}

// TestOExemploCasaComOQueONoEnvia fecha o circulo entre configuracao e simulacao.
//
// O exemplo distribuido configura os quatro canais que o synkacore-no de fato
// emite. Se os dois divergirem, quem seguir o guia de "como rodar" vai ver um
// relatorio de comissionamento cheio de divergencia logo na primeira execucao — e
// vai concluir, com razao, que o sistema esta quebrado.
func TestOExemploCasaComOQueONoEnvia(t *testing.T) {
	configurada, err := configuracaoarquivo.Carregar(caminhoDoExemplo)
	if err != nil {
		t.Fatalf("carregamento falhou: %v", err)
	}

	dispositivo, err := identidadededispositivo.AnalisarIDDoDispositivo("camara-de-vacuo-01")
	if err != nil {
		t.Fatalf("dispositivo invalido: %v", err)
	}

	// Os canais que o no declara em seu descritor.
	temperatura, _ := instalacao.AnalisarGrandeza("temperatura")
	pressao, _ := instalacao.AnalisarGrandeza("pressao")
	estado, _ := instalacao.AnalisarGrandeza("estado_digital")
	contagem, _ := instalacao.AnalisarGrandeza("contagem_de_pecas")

	declarados := []instalacao.CanalDeclarado{
		{Endereco: aquisicao.EnderecoDeCanal{IndiceDoCanal: 0}, Grandeza: temperatura, Unidade: "Cel"},
		{Endereco: aquisicao.EnderecoDeCanal{IndiceDoCanal: 1}, Grandeza: pressao, Unidade: "kPa"},
		{Endereco: aquisicao.EnderecoDeCanal{IndiceDoCanal: 2}, Grandeza: estado, Unidade: "1"},
		{Endereco: aquisicao.EnderecoDeCanal{IndiceDoCanal: 3}, Grandeza: contagem, Unidade: "1"},
	}

	if divergencias := configurada.ConferirDescritor(dispositivo, declarados, time.Now()); len(divergencias) != 0 {
		for _, divergencia := range divergencias {
			t.Errorf("o exemplo diverge do que o no envia: %s em %s (declarado %q, esperado %q)",
				divergencia.Especie, divergencia.Canal, divergencia.Declarado, divergencia.Esperado)
		}
	}
}

func escreverTemporario(t *testing.T, conteudo string) string {
	t.Helper()
	caminho := filepath.Join(t.TempDir(), "instalacao.yaml")
	if err := os.WriteFile(caminho, []byte(conteudo), 0o600); err != nil {
		t.Fatalf("escrita do arquivo de teste falhou: %v", err)
	}
	return caminho
}

const configuracaoMinima = `
instalacao: planta-teste
pontos_de_medicao:
  - dispositivo: camara-01
    canal: 0
    ponto: curtimento.camara-01.temperatura
    grandeza: temperatura
    unidade: Cel
`

// TestCampoDesconhecidoDerrubaOCarregamento e a razao de a decodificacao ser estrita.
//
// Num decodificador tolerante, `unidad:` em vez de `unidade:` seria silenciosamente
// ignorado, o ponto ficaria sem unidade e o gateway subiria com dado em escala
// indefinida. Falhar na partida com "campo desconhecido" e infinitamente melhor.
func TestCampoDesconhecidoDerrubaOCarregamento(t *testing.T) {
	caminho := escreverTemporario(t, `
instalacao: planta-teste
pontos_de_medicao:
  - dispositivo: camara-01
    canal: 0
    ponto: curtimento.camara-01.temperatura
    grandeza: temperatura
    unidad: Cel
`)

	if _, err := configuracaoarquivo.Carregar(caminho); err == nil {
		t.Fatal("campo desconhecido deveria derrubar o carregamento")
	} else if !falha.TemCategoria(err, falha.CategoriaEntradaInvalida) {
		t.Errorf("categoria = %v", falha.CategoriaDe(err))
	}
}

// TestErroApontaAPosicaoNaLista verifica a ergonomia da mensagem.
//
// O erro pode estar numa lista de duzentos canais. "Dispositivo invalido" sem
// posicao obriga quem le a caçar linha por linha.
func TestErroApontaAPosicaoNaLista(t *testing.T) {
	caminho := escreverTemporario(t, `
instalacao: planta-teste
pontos_de_medicao:
  - dispositivo: camara-01
    canal: 0
    ponto: curtimento.camara-01.temperatura
    grandeza: temperatura
    unidade: Cel
  - dispositivo: Camara_02
    canal: 0
    ponto: curtimento.camara-02.temperatura
    grandeza: temperatura
    unidade: Cel
`)

	_, err := configuracaoarquivo.Carregar(caminho)
	if err == nil {
		t.Fatal("dispositivo fora do alfabeto deveria ser recusado")
	}
	if !strings.Contains(err.Error(), "ponto_de_medicao[1]") {
		t.Errorf("a mensagem nao aponta a posicao na lista: %v", err)
	}
}

func TestCanalRepetidoERecusado(t *testing.T) {
	caminho := escreverTemporario(t, `
instalacao: planta-teste
pontos_de_medicao:
  - dispositivo: camara-01
    canal: 0
    ponto: curtimento.camara-01.temperatura
    grandeza: temperatura
    unidade: Cel
  - dispositivo: camara-01
    canal: 0
    ponto: curtimento.camara-01.pressao
    grandeza: pressao
    unidade: kPa
`)

	if _, err := configuracaoarquivo.Carregar(caminho); err == nil {
		t.Fatal("dois pontos no mesmo canal deveriam ser recusados")
	}
}

func TestArquivoAusenteEEncontravelPelaMensagem(t *testing.T) {
	_, err := configuracaoarquivo.Carregar("/tmp/nao-existe-synkacore/instalacao.yaml")
	if err == nil {
		t.Fatal("arquivo ausente deveria falhar")
	}
	if !falha.TemCategoria(err, falha.CategoriaNaoEncontrado) {
		t.Errorf("categoria = %v, esperado CategoriaNaoEncontrado", falha.CategoriaDe(err))
	}
	if !strings.Contains(err.Error(), "instalacao.yaml") {
		t.Errorf("a mensagem nao diz qual caminho falhou: %v", err)
	}
}

func TestConfiguracaoMinimaCarrega(t *testing.T) {
	configurada, err := configuracaoarquivo.Carregar(escreverTemporario(t, configuracaoMinima))
	if err != nil {
		t.Fatalf("configuracao minima deveria carregar: %v", err)
	}

	// Sem catalogo de motivos declarado, a versao e zero — e isso nao acusa deriva
	// contra uma origem que tambem nao declara.
	if !configurada.ConferirVersaoDoCatalogoDeMotivos(0) {
		t.Error("instalacao sem catalogo nao deveria acusar deriva")
	}
}
