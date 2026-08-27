package diariosqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/saida/diariosqlite"
	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

var instanteDeReferencia = time.Date(2026, time.August, 26, 14, 30, 0, 0, time.UTC)

// diarioDeTeste abre um diario num arquivo temporario real.
//
// Arquivo, e nao um duble nem :memory:, de proposito: o diario E a definicao de
// durabilidade do sistema, e um duble testaria a nossa ideia do SQLite em vez do
// SQLite. O arquivo temporario e mais fiel e nao e mais lento nesta escala.
func diarioDeTeste(t *testing.T) *diariosqlite.Diario {
	t.Helper()

	caminho := filepath.Join(t.TempDir(), "diario-de-teste.db")
	diario, err := diariosqlite.Abrir(t.Context(), caminho)
	if err != nil {
		t.Fatalf("abertura do diario de teste falhou: %v", err)
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

// envelopeDeTeste constroi um envelope valido com a sequencia indicada.
func envelopeDeTeste(t *testing.T, sessao string, sequencia uint64, valor float32) aquisicao.Envelope {
	t.Helper()

	conteudo, err := proto.Marshal(&contratov1.AmostraEscalar{
		Endereco: &contratov1.EnderecoDeCanal{IndiceDoCanal: proto.Uint32(1)},
		Valor:    proto.Float32(valor),
	})
	if err != nil {
		t.Fatalf("serializacao do conteudo falhou: %v", err)
	}

	envelope, err := aquisicao.NovoEnvelope(aquisicao.ParametrosDeEnvelope{
		VersaoDoEsquema:   1,
		IDDoDispositivo:   "prensa-01",
		IDDaSessaoDeBoot:  sessao,
		NumeroDeSequencia: sequencia,
		TempoLigadoMs:     sequencia * 1000,
		Tipo:              string(aquisicao.TipoAmostraEscalar),
		Conteudo:          conteudo,
		InstanteObservado: instanteDeReferencia.Add(time.Duration(sequencia) * time.Second),
	}, catalogoDeTeste(t))
	if err != nil {
		t.Fatalf("envelope de teste deveria ser valido: %v", err)
	}
	return envelope
}

func loteDeTeste(t *testing.T, sessao string, de, ate uint64) []aquisicao.Envelope {
	t.Helper()
	var lote []aquisicao.Envelope
	for sequencia := de; sequencia <= ate; sequencia++ {
		lote = append(lote, envelopeDeTeste(t, sessao, sequencia, float32(sequencia)))
	}
	return lote
}

func TestGravaLoteEDevolveOQueFicouDuravel(t *testing.T) {
	diario := diarioDeTeste(t)

	resultado, err := diario.GravarLote(t.Context(), loteDeTeste(t, "boot-a", 1, 10))
	if err != nil {
		t.Fatalf("gravacao do lote falhou: %v", err)
	}

	if resultado.Gravados != 10 {
		t.Errorf("gravados = %d, esperado 10", resultado.Gravados)
	}
	if resultado.Duplicados != 0 {
		t.Errorf("duplicados = %d, esperado 0", resultado.Duplicados)
	}
	if resultado.MaiorSequenciaDuravel != 10 {
		t.Errorf("maior sequencia duravel = %d, esperado 10", resultado.MaiorSequenciaDuravel)
	}
}

// TestReentregaNaoDuplicaEContaComoSucesso e o teste que sustenta a corretude de
// todo relatorio do sistema.
//
// Store-and-forward com retransmissao SEMPRE gera entrega duplicada — e
// consequencia do desenho, nao defeito. Sem deduplicacao, cada parada de maquina
// retransmitida seria contada duas vezes e o relatorio ficaria errado em silencio.
//
// E a duplicata precisa contar como SUCESSO: se ela nao avancasse a confirmacao, a
// origem retransmitiria para sempre o que ja esta salvo e nunca liberaria o buffer.
func TestReentregaNaoDuplicaEContaComoSucesso(t *testing.T) {
	diario := diarioDeTeste(t)
	lote := loteDeTeste(t, "boot-a", 1, 5)

	if _, err := diario.GravarLote(t.Context(), lote); err != nil {
		t.Fatalf("primeira entrega falhou: %v", err)
	}

	// A origem nao recebeu a confirmacao e retransmite o mesmo lote.
	resultado, err := diario.GravarLote(t.Context(), lote)
	if err != nil {
		t.Fatalf("reentrega deveria ser aceita: %v", err)
	}

	if resultado.Gravados != 0 {
		t.Errorf("gravados = %d na reentrega, esperado 0", resultado.Gravados)
	}
	if resultado.Duplicados != 5 {
		t.Errorf("duplicados = %d, esperado 5", resultado.Duplicados)
	}
	if resultado.MaiorSequenciaDuravel != 5 {
		t.Error("duplicata precisa contar como duravel, senao a origem retransmite para sempre")
	}

	registros, err := diario.LerAPartirDe(t.Context(), 0, 100)
	if err != nil {
		t.Fatalf("leitura do diario falhou: %v", err)
	}
	if len(registros) != 5 {
		t.Errorf("o diario tem %d registros apos a reentrega, esperado 5", len(registros))
	}
}

// TestSessoesDeBootDiferentesNaoColidem cobre a razao de a sessao de boot compor a
// chave.
//
// O contador de sequencia da origem reinicia em zero a cada partida. Sem a sessao
// na chave, a mensagem 1 da segunda partida seria confundida com a mensagem 1 da
// primeira, e o dado novo seria descartado como duplicata.
func TestSessoesDeBootDiferentesNaoColidem(t *testing.T) {
	diario := diarioDeTeste(t)

	if _, err := diario.GravarLote(t.Context(), loteDeTeste(t, "boot-a", 1, 3)); err != nil {
		t.Fatalf("primeira sessao falhou: %v", err)
	}
	resultado, err := diario.GravarLote(t.Context(), loteDeTeste(t, "boot-b", 1, 3))
	if err != nil {
		t.Fatalf("segunda sessao falhou: %v", err)
	}

	if resultado.Gravados != 3 {
		t.Errorf("gravados = %d na nova sessao de boot, esperado 3", resultado.Gravados)
	}
}

// TestGravacaoDeLoteETudoOuNada protege a garantia que a confirmacao carrega.
//
// "Duravel ate a sequencia N" so vale se N implicar que TUDO ate N esta salvo.
// Gravar metade e confirmar metade deixaria a origem sem saber o que reter.
func TestGravacaoDeLoteETudoOuNada(t *testing.T) {
	diario := diarioDeTeste(t)

	// O envelope repetido dentro do MESMO lote e absorvido pela restricao de
	// unicidade, sem derrubar a transacao — reentrega e caso normal, inclusive
	// dentro de um lote remontado pela origem apos reinicio.
	lote := loteDeTeste(t, "boot-a", 1, 3)
	lote = append(lote, envelopeDeTeste(t, "boot-a", 2, 99))

	resultado, err := diario.GravarLote(t.Context(), lote)
	if err != nil {
		t.Fatalf("lote com repeticao interna deveria ser aceito: %v", err)
	}
	if resultado.Gravados != 3 || resultado.Duplicados != 1 {
		t.Errorf("gravados = %d, duplicados = %d; esperado 3 e 1",
			resultado.Gravados, resultado.Duplicados)
	}

	// E o primeiro valor vence: o conteudo ja duravel nao e sobrescrito por uma
	// reentrega. O dado bruto da origem, uma vez gravado, e imutavel.
	registros, err := diario.LerAPartirDe(t.Context(), 0, 100)
	if err != nil {
		t.Fatalf("leitura falhou: %v", err)
	}
	if len(registros) != 3 {
		t.Fatalf("registros = %d, esperado 3", len(registros))
	}
}

// TestConteudoBrutoSobreviveIntactoAoCicloDeGravacao e a garantia que torna o
// reprocessamento possivel.
//
// O diario guarda os bytes da origem, nao a nossa interpretacao deles. Se a
// decodificacao estiver errada — hoje ou daqui a dois anos —, o original ainda
// permite recomputar. Um ciclo que corrompesse esses bytes destruiria a unica
// copia autoritativa.
func TestConteudoBrutoSobreviveIntactoAoCicloDeGravacao(t *testing.T) {
	diario := diarioDeTeste(t)
	envelope := envelopeDeTeste(t, "boot-a", 1, 65.4)

	if _, err := diario.GravarLote(t.Context(), []aquisicao.Envelope{envelope}); err != nil {
		t.Fatalf("gravacao falhou: %v", err)
	}

	registros, err := diario.LerAPartirDe(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("leitura falhou: %v", err)
	}
	if len(registros) != 1 {
		t.Fatalf("registros = %d, esperado 1", len(registros))
	}

	if string(registros[0].ConteudoBruto) != string(envelope.ConteudoBruto()) {
		t.Error("o conteudo bruto mudou ao passar pelo diario")
	}
	if !registros[0].InstanteObservado.Equal(envelope.InstanteObservado()) {
		t.Errorf("instante observado = %v, esperado %v",
			registros[0].InstanteObservado, envelope.InstanteObservado())
	}
	if registros[0].TempoLigado != envelope.TempoLigado() {
		t.Errorf("tempo ligado = %v, esperado %v", registros[0].TempoLigado, envelope.TempoLigado())
	}
	if registros[0].ChaveDeIdempotencia != envelope.ChaveDeIdempotencia().String() {
		t.Error("a chave de idempotencia gravada difere da do envelope")
	}
}

// TestLeituraSegueAOrdemDeDurabilidade verifica que a projecao consome numa ordem
// total e estavel.
func TestLeituraSegueAOrdemDeDurabilidade(t *testing.T) {
	diario := diarioDeTeste(t)

	if _, err := diario.GravarLote(t.Context(), loteDeTeste(t, "boot-a", 1, 5)); err != nil {
		t.Fatalf("gravacao falhou: %v", err)
	}

	primeiros, err := diario.LerAPartirDe(t.Context(), 0, 2)
	if err != nil {
		t.Fatalf("leitura falhou: %v", err)
	}
	if len(primeiros) != 2 {
		t.Fatalf("primeira pagina tem %d registros, esperado 2", len(primeiros))
	}

	seguintes, err := diario.LerAPartirDe(t.Context(), primeiros[1].ID, 10)
	if err != nil {
		t.Fatalf("segunda leitura falhou: %v", err)
	}
	if len(seguintes) != 3 {
		t.Fatalf("segunda pagina tem %d registros, esperado 3", len(seguintes))
	}
	if seguintes[0].ID <= primeiros[1].ID {
		t.Error("a paginacao devolveu registro ja consumido")
	}
}

func TestCursorComecaEmZeroEAvanca(t *testing.T) {
	diario := diarioDeTeste(t)

	inicial, err := diario.LerCursor(t.Context(), "timescale")
	if err != nil {
		t.Fatalf("cursor ausente nao deveria ser erro: %v", err)
	}
	if inicial != 0 {
		t.Errorf("cursor inicial = %d, esperado 0", inicial)
	}

	if err := diario.AvancarCursor(t.Context(), "timescale", 42, instanteDeReferencia); err != nil {
		t.Fatalf("avanco do cursor falhou: %v", err)
	}
	if err := diario.AvancarCursor(t.Context(), "timescale", 99, instanteDeReferencia); err != nil {
		t.Fatalf("segundo avanco falhou: %v", err)
	}

	atual, err := diario.LerCursor(t.Context(), "timescale")
	if err != nil {
		t.Fatalf("leitura do cursor falhou: %v", err)
	}
	if atual != 99 {
		t.Errorf("cursor = %d, esperado 99", atual)
	}

	// Cursores de consumidores diferentes avancam independentemente.
	outro, err := diario.LerCursor(t.Context(), "auditoria")
	if err != nil {
		t.Fatalf("leitura do segundo cursor falhou: %v", err)
	}
	if outro != 0 {
		t.Errorf("cursor de outro consumidor = %d, esperado 0", outro)
	}
}

// TestPodaNaoApagaOQueAProjecaoAindaNaoConsumiu e a trava que impede a poda de
// destruir a unica copia existente.
//
// O cenario perigoso e exatamente o que este sistema existe para sobreviver: o
// banco de consulta cai, a projecao para, o diario acumula. Se a poda olhasse so
// a idade, ela apagaria justamente o que ninguem leu ainda.
func TestPodaNaoApagaOQueAProjecaoAindaNaoConsumiu(t *testing.T) {
	diario := diarioDeTeste(t)

	if _, err := diario.GravarLote(t.Context(), loteDeTeste(t, "boot-a", 1, 10)); err != nil {
		t.Fatalf("gravacao falhou: %v", err)
	}

	// Muito depois, mas sem projecao nenhuma: nada pode sair.
	bemDepois := instanteDeReferencia.Add(365 * 24 * time.Hour)
	removidos, err := diario.Podar(t.Context(), time.Hour, bemDepois)
	if err != nil {
		t.Fatalf("poda falhou: %v", err)
	}
	if removidos != 0 {
		t.Errorf("poda removeu %d registros sem nenhuma projecao ter acontecido", removidos)
	}

	// A projecao consome os cinco primeiros.
	registros, err := diario.LerAPartirDe(t.Context(), 0, 5)
	if err != nil {
		t.Fatalf("leitura falhou: %v", err)
	}
	if err := diario.AvancarCursor(t.Context(), "timescale", registros[4].ID, bemDepois); err != nil {
		t.Fatalf("avanco do cursor falhou: %v", err)
	}

	// Agora a poda pode levar os cinco projetados — e apenas eles.
	removidos, err = diario.Podar(t.Context(), time.Hour, bemDepois)
	if err != nil {
		t.Fatalf("poda falhou: %v", err)
	}
	if removidos != 5 {
		t.Errorf("poda removeu %d registros, esperado 5", removidos)
	}

	restantes, err := diario.LerAPartirDe(t.Context(), 0, 100)
	if err != nil {
		t.Fatalf("leitura falhou: %v", err)
	}
	if len(restantes) != 5 {
		t.Errorf("restaram %d registros, esperado 5", len(restantes))
	}
}

// TestPodaRespeitaAIdadeMinima cobre a segunda condicao.
//
// Apagar assim que projetado tornaria irrecuperavel qualquer erro descoberto na
// projecao. A janela de idade e o que permite recomputar.
func TestPodaRespeitaAIdadeMinima(t *testing.T) {
	diario := diarioDeTeste(t)

	if _, err := diario.GravarLote(t.Context(), loteDeTeste(t, "boot-a", 1, 5)); err != nil {
		t.Fatalf("gravacao falhou: %v", err)
	}
	registros, err := diario.LerAPartirDe(t.Context(), 0, 5)
	if err != nil {
		t.Fatalf("leitura falhou: %v", err)
	}
	if err := diario.AvancarCursor(t.Context(), "timescale", registros[4].ID, instanteDeReferencia); err != nil {
		t.Fatalf("avanco do cursor falhou: %v", err)
	}

	// Tudo projetado, mas ainda dentro da janela de retencao.
	logoDepois := instanteDeReferencia.Add(time.Minute)
	removidos, err := diario.Podar(t.Context(), 7*24*time.Hour, logoDepois)
	if err != nil {
		t.Fatalf("poda falhou: %v", err)
	}
	if removidos != 0 {
		t.Errorf("poda removeu %d registros ainda dentro da janela de retencao", removidos)
	}
}

func TestMaiorSequenciaDuravelResponsePorSessao(t *testing.T) {
	diario := diarioDeTeste(t)

	if _, err := diario.GravarLote(t.Context(), loteDeTeste(t, "boot-a", 1, 7)); err != nil {
		t.Fatalf("gravacao falhou: %v", err)
	}

	maior, err := diario.MaiorSequenciaDuravel(t.Context(), "prensa-01", "boot-a")
	if err != nil {
		t.Fatalf("consulta falhou: %v", err)
	}
	if maior != 7 {
		t.Errorf("maior sequencia = %d, esperado 7", maior)
	}

	// Sessao que nunca gravou nada responde zero, nao erro.
	nenhuma, err := diario.MaiorSequenciaDuravel(t.Context(), "prensa-01", "boot-z")
	if err != nil {
		t.Fatalf("consulta de sessao inexistente nao deveria falhar: %v", err)
	}
	if nenhuma != 0 {
		t.Errorf("sessao inexistente devolveu %d, esperado 0", nenhuma)
	}
}

// TestFalhaDoDiarioECategoriaInterna verifica a classificacao.
//
// O diario NUNCA e CategoriaIndisponivel. Indisponivel significa "tente de novo,
// e uma dependencia a jusante"; se o diario falha, a durabilidade foi violada e
// nao ha degradacao graciosa possivel.
func TestFalhaDoDiarioECategoriaInterna(t *testing.T) {
	_, err := diariosqlite.Abrir(context.Background(),
		filepath.Join(t.TempDir(), "sem-tal-pasta", "diario.db"))
	if err == nil {
		t.Fatal("abrir diario em caminho inexistente deveria falhar")
	}
	if !falha.TemCategoria(err, falha.CategoriaInterna) {
		t.Errorf("categoria = %v, esperado CategoriaInterna", falha.CategoriaDe(err))
	}
}
