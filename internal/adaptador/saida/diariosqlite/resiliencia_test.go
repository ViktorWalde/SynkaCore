package diariosqlite_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/saida/diariosqlite"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

// diarioComTeto abre um diario com o teto de bytes indicado.
func diarioComTeto(t *testing.T, teto int64) *diariosqlite.Diario {
	t.Helper()

	diario, err := diariosqlite.Abrir(t.Context(), filepath.Join(t.TempDir(), "diario.db"))
	if err != nil {
		t.Fatalf("abertura do diario falhou: %v", err)
	}
	t.Cleanup(func() { _ = diario.Fechar() })
	return diario.ComTetoDeBytes(teto)
}

// TestSemProjecaoAPodaNaoRemoveNada documenta o defeito que motivou o teto.
//
// A poda so apaga o que esta abaixo do MENOR CURSOR de projecao. Sem projecao
// configurada, `cursor_de_projecao` esta vazio, o MIN devolve NULO, e a funcao
// retorna zero sem apagar nada — para sempre.
//
// O problema e onde isso morde: essa e exatamente a configuracao que o projeto
// recomenda para comecar e para comissionar. Um gateway deixado "so adquirindo"
// cresce sem limite ate encher o disco, e o proprio log da partida diz "a aquisicao
// funciona normalmente e o dado fica duravel no diario" sem mencionar que tambem
// nunca sera podado.
//
// Este teste NAO trava um comportamento a corrigir: ele trava a razao de o teto de
// tamanho existir. Se um dia a poda passar a funcionar sem cursor, este teste falha e
// obriga quem mudou a reavaliar o teto junto.
func TestSemProjecaoAPodaNaoRemoveNada(t *testing.T) {
	t.Parallel()

	diario := diarioComTeto(t, 0)
	gravarLoteDeTeste(t, diario, "boot-poda", 10)

	// Muito alem de qualquer retencao: se a idade bastasse, tudo sairia.
	removidos, err := diario.Podar(t.Context(), time.Hour, instanteDeReferencia.Add(10*365*24*time.Hour))
	if err != nil {
		t.Fatalf("poda falhou: %v", err)
	}
	if removidos != 0 {
		t.Fatalf("removidos = %d; sem cursor de projecao a poda nao deveria remover nada", removidos)
	}

	registros, err := diario.LerAPartirDe(t.Context(), 0, 100)
	if err != nil {
		t.Fatalf("leitura falhou: %v", err)
	}
	if len(registros) != 10 {
		t.Fatalf("registros = %d, esperado 10 intactos", len(registros))
	}
}

// TestTetoRecusaGravacaoEmVezDeEncherODisco e a correcao.
//
// A recusa e a resposta certa porque ela e RECUPERAVEL: a origem preserva o lote e
// retransmite, e o dado espera do outro lado. Encher o disco nao e recuperavel pelo
// gateway — o SQLite passa a falhar, o log para de ser escrito, e a maquina inteira
// vai junto.
func TestTetoRecusaGravacaoEmVezDeEncherODisco(t *testing.T) {
	t.Parallel()

	// Um byte: qualquer diario ja criado excede.
	diario := diarioComTeto(t, 1)

	if _, err := diario.MedirOcupacao(); err != nil {
		t.Fatalf("medicao falhou: %v", err)
	}

	_, err := diario.GravarLote(t.Context(), []aquisicao.Envelope{
		envelopeDeTeste(t, "boot-teto", 1, 24.5),
	})
	if !falha.TemCategoria(err, falha.CategoriaArmazenamentoEsgotado) {
		t.Fatalf("categoria = %v, esperado armazenamento esgotado", falha.CategoriaDe(err))
	}

	// A mensagem precisa dizer o que FAZER. Um erro que so informa o estado obriga
	// quem esta de plantao a descobrir a acao sozinho, as tres da manha.
	for _, termo := range []string{"teto", "projecao", "libere"} {
		if !strings.Contains(err.Error(), termo) {
			t.Errorf("a mensagem nao diz o que fazer (falta %q): %v", termo, err)
		}
	}
}

// TestSemTetoConfiguradoNadaERecusado delimita a trava.
//
// Teto zero desliga a verificacao. Uma maquina com disco dedicado ao diario pode
// preferir isso, e o gateway nao deve impor um limite que ninguem pediu.
func TestSemTetoConfiguradoNadaERecusado(t *testing.T) {
	t.Parallel()

	diario := diarioComTeto(t, 0)
	if _, err := diario.MedirOcupacao(); err != nil {
		t.Fatalf("medicao falhou: %v", err)
	}

	if _, err := diario.GravarLote(t.Context(), []aquisicao.Envelope{
		envelopeDeTeste(t, "boot-sem-teto", 1, 24.5),
	}); err != nil {
		t.Fatalf("sem teto a gravacao deveria passar: %v", err)
	}
}

// TestOcupacaoContaOsArquivosDoModoWAL trava a medida.
//
// Medir so o `.db` subestimaria a ocupacao justamente sob carga, que e quando o WAL
// cresce entre checkpoints — e o teto erraria para o lado perigoso, deixando o disco
// encher enquanto o gateway se acha dentro do limite.
func TestOcupacaoContaOsArquivosDoModoWAL(t *testing.T) {
	t.Parallel()

	diario := diarioComTeto(t, 0)

	vazio, err := diario.MedirOcupacao()
	if err != nil {
		t.Fatalf("medicao falhou: %v", err)
	}

	gravarLoteDeTeste(t, diario, "boot-ocupacao", 200)

	cheio, err := diario.MedirOcupacao()
	if err != nil {
		t.Fatalf("medicao falhou: %v", err)
	}
	if cheio <= vazio {
		t.Fatalf("ocupacao nao cresceu apos gravar: antes %d, depois %d", vazio, cheio)
	}
}

// TestVerificarProvaEscritaENaoSoLeitura e a segunda correcao da versao.
//
// A verificacao anterior executava apenas um SELECT. Num disco cheio o SELECT
// continua funcionando enquanto todo INSERT falha — e o gateway reportaria
// `journal: available` recusando toda remessa, violando no modo de falha MAIS
// PROVAVEL a regra que o proprio projeto escreveu.
//
// O que este teste consegue travar e o lado positivo: a sonda de fato grava, confirma
// a transacao e NAO deixa residuo. O disco cheio em si continua sem exercicio
// automatizado, e isso esta declarado em vez de omitido.
func TestVerificarProvaEscritaENaoSoLeitura(t *testing.T) {
	t.Parallel()

	diario := diarioComTeto(t, 0)

	for range 3 {
		if err := diario.Verificar(t.Context()); err != nil {
			t.Fatalf("verificacao falhou num diario saudavel: %v", err)
		}
	}

	// A sonda usa a tabela de calibracao, e nao o diario: uma linha fabricada que
	// escapasse para o diario seria PROJETADA ao modelo de leitura como se fosse
	// medicao.
	pendentes, err := diario.ContarCalibracoesPendentes(t.Context())
	if err != nil {
		t.Fatalf("contagem falhou: %v", err)
	}
	if pendentes != 0 {
		t.Fatalf("a sonda de saude deixou %d linhas para tras", pendentes)
	}

	registros, err := diario.LerAPartirDe(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("leitura falhou: %v", err)
	}
	if len(registros) != 0 {
		t.Fatalf("a sonda de saude gravou %d linhas NO DIARIO", len(registros))
	}
}

// TestVerificarFalhaComDiarioFechado trava a deteccao de indisponibilidade.
func TestVerificarFalhaComDiarioFechado(t *testing.T) {
	t.Parallel()

	diario, err := diariosqlite.Abrir(t.Context(), filepath.Join(t.TempDir(), "diario.db"))
	if err != nil {
		t.Fatalf("abertura falhou: %v", err)
	}
	if err := diario.Fechar(); err != nil {
		t.Fatalf("fechamento falhou: %v", err)
	}

	if err := diario.Verificar(t.Context()); err == nil {
		t.Fatal("verificacao de um diario fechado deveria falhar")
	}
}

// gravarLoteDeTeste grava N envelopes sequenciais de uma sessao.
func gravarLoteDeTeste(t *testing.T, diario *diariosqlite.Diario, sessao string, quantidade int) {
	t.Helper()

	envelopes := make([]aquisicao.Envelope, 0, quantidade)
	for sequencia := 1; sequencia <= quantidade; sequencia++ {
		envelopes = append(envelopes, envelopeDeTeste(t, sessao, uint64(sequencia), 24.5))
	}
	if _, err := diario.GravarLote(t.Context(), envelopes); err != nil {
		t.Fatalf("gravacao falhou: %v", err)
	}
}
