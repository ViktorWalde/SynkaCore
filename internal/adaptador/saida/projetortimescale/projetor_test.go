package projetortimescale_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/saida/projetortimescale"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/identificador"
)

// variavelDoBanco aponta para o banco de consulta contra o qual medir.
//
// EXIGIDA, e o teste PULA sem ela — a mesma disciplina do benchmark do diario, que
// exige SYNKACORE_DISCO_DE_MEDICAO e se recusa a rodar em tmpfs. Recusar-se a
// responder e melhor que responder com confianca infundada, e um teste que "passa"
// contra um duble de Postgres afirmaria justamente o que nao verificou.
//
// O alvo `make testar-projecao` sobe a infraestrutura e FALHA se ela nao estiver de
// pe: o teste pula, o alvo insiste. Sem essa assimetria, `make verificar` exigiria
// Docker de todo mundo — e um portao que nao roda na maquina de quem desenvolve e
// um portao que sera contornado.
const variavelDoBanco = "SYNKACORE_BANCO_DE_TESTE"

// caminhoDasMigracoes aponta para o esquema versionado do projeto.
const caminhoDasMigracoes = "../../../../migracoes"

// bancoDeTeste devolve um projetor sobre um ESQUEMA PROPRIO, recem-migrado.
//
// Esquema proprio por execucao, e nao a tabela compartilhada, por duas razoes que
// se somam:
//
//  1. Isolamento. Os testes rodam em paralelo e o modelo de leitura nao tem chave
//     por teste; sem separacao, um veria as linhas do outro.
//  2. E a unica forma de exercitar as MIGRACOES DO ZERO. No docker-compose elas
//     rodam apenas na criacao do volume, entao uma migracao quebrada so apareceria
//     numa instalacao nova — na maquina do cliente, e nao aqui.
func bancoDeTeste(t *testing.T) (*projetortimescale.Projetor, *pgxpool.Pool, string) {
	t.Helper()
	return bancoComMigracoes(t, todasAsMigracoes)
}

// todasAsMigracoes pede o esquema completo.
const todasAsMigracoes = 0

// bancoComMigracoes monta o esquema com apenas as N primeiras migracoes aplicadas.
//
// Aplicar um PREFIXO das migracoes nao e artificio de teste: e o estado real de um
// banco que ficou para tras quando o binario foi atualizado, que e a coisa mais
// comum de acontecer numa planta atualizada pelo notebook de um tecnico.
func bancoComMigracoes(t *testing.T, quantas int) (*projetortimescale.Projetor, *pgxpool.Pool, string) {
	t.Helper()

	url := os.Getenv(variavelDoBanco)
	if url == "" {
		t.Skip(variavelDoBanco + " ausente: o teste do modelo de leitura exige um " +
			"TimescaleDB de verdade. Rode `make testar-projecao`")
	}

	esquema := "teste_" + strings.ReplaceAll(identificador.Sortear(""), "-", "")
	administrativo := conectar(t, url)

	if _, err := administrativo.Exec(t.Context(), `CREATE SCHEMA `+esquema); err != nil {
		t.Fatalf("criacao do esquema falhou: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancelar := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelar()
		_, _ = administrativo.Exec(ctx, `DROP SCHEMA `+esquema+` CASCADE`)
		administrativo.Close()
	})

	urlDoEsquema := comEsquema(url, esquema)
	aplicarMigracoes(t, urlDoEsquema, quantas)

	projetor, err := projetortimescale.Abrir(t.Context(), urlDoEsquema)
	if err != nil {
		t.Fatalf("abertura do projetor falhou: %v", err)
	}
	t.Cleanup(projetor.Fechar)

	return projetor, conectarECuidar(t, urlDoEsquema), esquema
}

func conectar(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatalf("conexao falhou: %v", err)
	}
	return pool
}

func conectarECuidar(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	pool := conectar(t, url)
	t.Cleanup(pool.Close)
	return pool
}

// comEsquema acrescenta o search_path a URL de conexao.
//
// O esquema do teste vem PRIMEIRO e `public` continua no caminho, e essa segunda
// parte custou a primeira execucao deste teste: sem ela, a migracao 0001 falha com
// `function by_range(unknown) does not exist`. As funcoes da extensao TimescaleDB
// vivem em public, e um search_path que so aponta para o esquema do teste as torna
// invisiveis.
//
// Nao e defeito da migracao — em producao ela roda com o search_path padrao, onde
// public sempre esteve. E o isolamento do teste que precisa ser ADITIVO: criar um
// lugar proprio para as tabelas sem esconder o resto do banco.
func comEsquema(url, esquema string) string {
	separador := "?"
	if strings.Contains(url, "?") {
		separador = "&"
	}
	return url + separador + "search_path=" + esquema + ",public"
}

// aplicarMigracoes roda migracoes/ na ordem, contra o esquema do teste.
//
// Os arquivos REAIS que a instalacao usa, e nao um DDL escrito a mao aqui. Um
// esquema de teste proprio testaria a nossa ideia do modelo de leitura em vez do
// modelo de leitura — e divergiria em silencio na primeira migracao que alguem
// esquecesse de refletir nos dois lugares.
func aplicarMigracoes(t *testing.T, url string, quantas int) {
	t.Helper()

	arquivos, err := filepath.Glob(filepath.Join(caminhoDasMigracoes, "*.sql"))
	if err != nil || len(arquivos) == 0 {
		t.Fatalf("nenhuma migracao encontrada em %s: %v", caminhoDasMigracoes, err)
	}
	// Ordem lexicografica e a ordem das migracoes: 0001 antes de 0002. E por isso
	// que elas sao numeradas com zeros a esquerda.
	sort.Strings(arquivos)

	if quantas > 0 && quantas < len(arquivos) {
		arquivos = arquivos[:quantas]
	}

	pool := conectarECuidar(t, url)
	for _, arquivo := range arquivos {
		sql, err := os.ReadFile(arquivo) //nolint:gosec // caminho fixo do repositorio
		if err != nil {
			t.Fatalf("leitura de %s falhou: %v", arquivo, err)
		}
		if _, err := pool.Exec(t.Context(), string(sql)); err != nil {
			t.Fatalf("migracao %s falhou: %v", filepath.Base(arquivo), err)
		}
	}
}

// linhaDeTeste monta uma linha completa, com todos os campos derivados presentes.
func linhaDeTeste(dispositivo string, sequencia int64) projetortimescale.LinhaProjetada {
	estimado := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	valor := 24.7
	ponto := "curtimento.camara-01.temperatura"
	grandeza := "temperatura"
	unidade := "Cel"
	dentroDaFaixa := false

	return projetortimescale.LinhaProjetada{
		InstanteObservado:  estimado.Add(time.Second),
		InstanteEstimado:   &estimado,
		TempoLigadoMs:      12345,
		IDDoDispositivo:    dispositivo,
		IDDaSessaoDeBoot:   "boot-1",
		NumeroDeSequencia:  sequencia,
		TipoDeConteudo:     "synkacore.contrato.v1.AmostraEscalar",
		ClasseDeDado:       "sample",
		NomeDoCampo:        "value",
		ValorNumerico:      &valor,
		IDDoPontoDeMedicao: &ponto,
		Grandeza:           &grandeza,
		Unidade:            &unidade,
		ForaDeFaixa:        &dentroDaFaixa,
	}
}

// TestAsMigracoesAplicamDoZero e o teste que faltava desde a V2.0.
//
// No docker-compose as migracoes rodam APENAS na criacao do volume. Numa maquina de
// desenvolvimento com o volume ja criado, uma migracao quebrada nao acusa nada — e
// so apareceria numa instalacao NOVA, que e a do cliente. Aqui elas rodam do zero a
// cada execucao.
func TestAsMigracoesAplicamDoZero(t *testing.T) {
	t.Parallel()

	projetor, _, _ := bancoDeTeste(t)

	// Verificar so passa com o esquema COMPLETO, incluindo a coluna da migracao
	// 0002. E a mesma checagem que o gateway faz na partida.
	if err := projetor.Verificar(t.Context()); err != nil {
		t.Fatalf("o esquema recem-migrado nao passa na verificacao de partida: %v", err)
	}
}

// TestProjetarGravaTodosOsCamposDaLinha trava o mapeamento coluna a coluna.
//
// Dezessete colunas escritas por posicao numa unica instrucao INSERT. Trocar duas
// de lugar produziria dado plausivel e errado — grandeza no lugar de unidade,
// digamos —, e nada acusaria: os dois sao TEXT e o banco aceitaria feliz.
func TestProjetarGravaTodosOsCamposDaLinha(t *testing.T) {
	t.Parallel()

	projetor, pool, _ := bancoDeTeste(t)
	dispositivo := "camara-" + identificador.Sortear("")

	if err := projetor.Projetar(t.Context(),
		[]projetortimescale.LinhaProjetada{linhaDeTeste(dispositivo, 1)}); err != nil {
		t.Fatalf("projecao falhou: %v", err)
	}

	var (
		tempoLigado                 int64
		sessao, tipo, classe, campo string
		valor                       float64
		ponto, grandeza, unidade    string
		foraDeFaixa                 bool
		estimado                    time.Time
	)
	err := pool.QueryRow(t.Context(), `
		SELECT id_da_sessao_de_boot, tempo_ligado_ms, tipo_de_conteudo, classe_de_dado,
		       nome_do_campo, valor_numerico, id_do_ponto_de_medicao, grandeza, unidade,
		       fora_de_faixa, instante_estimado
		  FROM leitura WHERE id_do_dispositivo = $1`, dispositivo).
		Scan(&sessao, &tempoLigado, &tipo, &classe, &campo, &valor,
			&ponto, &grandeza, &unidade, &foraDeFaixa, &estimado)
	if err != nil {
		t.Fatalf("leitura do modelo falhou: %v", err)
	}

	conferir := func(nome, obtido, esperado string) {
		t.Helper()
		if obtido != esperado {
			t.Errorf("%s = %q, esperado %q", nome, obtido, esperado)
		}
	}
	conferir("id_da_sessao_de_boot", sessao, "boot-1")
	conferir("tipo_de_conteudo", tipo, "synkacore.contrato.v1.AmostraEscalar")
	conferir("classe_de_dado", classe, "sample")
	conferir("nome_do_campo", campo, "value")
	conferir("id_do_ponto_de_medicao", ponto, "curtimento.camara-01.temperatura")
	conferir("grandeza", grandeza, "temperatura")
	conferir("unidade", unidade, "Cel")

	if tempoLigado != 12345 {
		t.Errorf("tempo_ligado_ms = %d, esperado 12345", tempoLigado)
	}
	if valor != 24.7 {
		t.Errorf("valor_numerico = %v, esperado 24.7", valor)
	}
	if foraDeFaixa {
		t.Error("fora_de_faixa = true, esperado false")
	}
}

// TestProjecaoRepetidaNaoDuplica e a propriedade de que TODO o desenho depende.
//
// O cursor de projecao avanca DEPOIS de a gravacao estar confirmada. Se o gateway
// cair entre as duas coisas, o intervalo e reprocessado — e reprocessar precisa ser
// inofensivo. Sem isso, cada queda duplicaria linhas e todo grafico de contagem
// ficaria permanentemente errado.
//
// Ate aqui isso era garantido por uma restricao de unicidade no SQL que NENHUM
// teste automatizado jamais exercitou. Era a afirmacao mais importante do sistema
// sustentada apenas por leitura de codigo.
func TestProjecaoRepetidaNaoDuplica(t *testing.T) {
	t.Parallel()

	projetor, pool, _ := bancoDeTeste(t)
	dispositivo := "camara-" + identificador.Sortear("")

	lote := []projetortimescale.LinhaProjetada{
		linhaDeTeste(dispositivo, 1),
		linhaDeTeste(dispositivo, 2),
		linhaDeTeste(dispositivo, 3),
	}

	for range 3 {
		if err := projetor.Projetar(t.Context(), lote); err != nil {
			t.Fatalf("projecao falhou: %v", err)
		}
	}

	var total int64
	if err := pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM leitura WHERE id_do_dispositivo = $1`, dispositivo).Scan(&total); err != nil {
		t.Fatalf("contagem falhou: %v", err)
	}
	if total != 3 {
		t.Fatalf("linhas = %d apos projetar o mesmo lote 3 vezes, esperado 3", total)
	}
}

// TestCanalSemConfiguracaoEGravadoComNulos trava a decisao de comissionamento.
//
// Canal que chega sem configuracao continua sendo gravado, com as colunas derivadas
// nulas, em vez de recusado. Recusar significaria perder dado durante o
// comissionamento — que e exatamente quando canal nao configurado acontece.
//
// NULO aqui significa "o gateway ainda nao sabe o que isto mede", e e informacao
// honesta.
func TestCanalSemConfiguracaoEGravadoComNulos(t *testing.T) {
	t.Parallel()

	projetor, pool, _ := bancoDeTeste(t)
	dispositivo := "camara-" + identificador.Sortear("")

	linha := linhaDeTeste(dispositivo, 1)
	linha.IDDoPontoDeMedicao = nil
	linha.Grandeza = nil
	linha.Unidade = nil
	linha.ForaDeFaixa = nil
	linha.InstanteEstimado = nil

	if err := projetor.Projetar(t.Context(),
		[]projetortimescale.LinhaProjetada{linha}); err != nil {
		t.Fatalf("canal sem configuracao deveria ser gravado, e falhou: %v", err)
	}

	var ponto, grandeza *string
	var foraDeFaixa *bool
	var estimado *time.Time
	err := pool.QueryRow(t.Context(), `
		SELECT id_do_ponto_de_medicao, grandeza, fora_de_faixa, instante_estimado
		  FROM leitura WHERE id_do_dispositivo = $1`, dispositivo).
		Scan(&ponto, &grandeza, &foraDeFaixa, &estimado)
	if err != nil {
		t.Fatalf("leitura falhou: %v", err)
	}

	if ponto != nil || grandeza != nil || foraDeFaixa != nil || estimado != nil {
		t.Fatalf("colunas derivadas deveriam ser nulas: ponto=%v grandeza=%v faixa=%v estimado=%v",
			ponto, grandeza, foraDeFaixa, estimado)
	}
}

// TestForaDeFaixaDistingueOsTresEstadosNoBanco trava a distincao ate o disco.
//
// Nulo e "sem faixa configurada", falso e "dentro da faixa", verdadeiro e "fora".
// Colapsar nulo e falso faria uma instalacao AINDA NAO CONFIGURADA parecer
// inteiramente saudavel no painel — e ninguem procuraria o que nao esta acusando.
//
// O dominio ja trava isso; este teste verifica que a distincao SOBREVIVE a
// gravacao, que e onde um driver descuidado a colapsaria.
func TestForaDeFaixaDistingueOsTresEstadosNoBanco(t *testing.T) {
	t.Parallel()

	projetor, pool, _ := bancoDeTeste(t)
	dispositivo := "camara-" + identificador.Sortear("")

	verdadeiro, falso := true, false
	semFaixa := linhaDeTeste(dispositivo, 1)
	semFaixa.ForaDeFaixa = nil
	dentro := linhaDeTeste(dispositivo, 2)
	dentro.ForaDeFaixa = &falso
	fora := linhaDeTeste(dispositivo, 3)
	fora.ForaDeFaixa = &verdadeiro

	if err := projetor.Projetar(t.Context(),
		[]projetortimescale.LinhaProjetada{semFaixa, dentro, fora}); err != nil {
		t.Fatalf("projecao falhou: %v", err)
	}

	linhas, err := pool.Query(t.Context(), `
		SELECT numero_de_sequencia, fora_de_faixa FROM leitura
		 WHERE id_do_dispositivo = $1 ORDER BY numero_de_sequencia`, dispositivo)
	if err != nil {
		t.Fatalf("consulta falhou: %v", err)
	}
	defer linhas.Close()

	// Guardado como PONTEIRO, e nao ja convertido para texto: a distincao que este
	// teste existe para travar e justamente entre nulo e falso, e qualquer conversao
	// intermediaria seria a oportunidade de colapsar os dois no proprio teste.
	observado := map[int64]*bool{}
	for linhas.Next() {
		var sequencia int64
		var valor *bool
		if err := linhas.Scan(&sequencia, &valor); err != nil {
			t.Fatalf("leitura falhou: %v", err)
		}
		observado[sequencia] = valor
	}
	if err := linhas.Err(); err != nil {
		t.Fatalf("percurso das linhas falhou: %v", err)
	}

	if observado[1] != nil {
		t.Errorf("sem faixa configurada deveria gravar NULO, veio %v", *observado[1])
	}
	if observado[2] == nil || *observado[2] {
		t.Errorf("dentro da faixa deveria gravar false, veio %v", observado[2])
	}
	if observado[3] == nil || !*observado[3] {
		t.Errorf("fora da faixa deveria gravar true, veio %v", observado[3])
	}
}

// TestVerificarDenunciaMigracaoFaltandoNaPartida trava a checagem que o gateway faz
// ao subir.
//
// O cenario e o mais comum de todos numa planta atualizada pelo notebook de um
// tecnico: o binario novo entra, e alguem esquece de aplicar a migracao. A tabela
// `leitura` existe, o banco responde ao ping, e TODA gravacao falharia depois — em
// operacao, com erro de coluna inexistente, horas depois de o tecnico ter ido embora.
//
// Descobrir na partida custa uma mensagem. Descobrir em operacao custa uma
// investigacao, e nesse meio-tempo o dado fica so no diario.
func TestVerificarDenunciaMigracaoFaltandoNaPartida(t *testing.T) {
	t.Parallel()

	projetor, _, _ := bancoComMigracoes(t, 1)

	err := projetor.Verificar(t.Context())
	if err == nil {
		t.Fatal("um banco sem a migracao 0002 deveria reprovar a verificacao de partida")
	}
	if !strings.Contains(err.Error(), "migracao 0002") {
		t.Errorf("a mensagem nao diz qual migracao falta: %v", err)
	}
}

// TestLoteFalhoNaoDeixaLinhaGravada trava a atomicidade da projecao.
//
// A projecao inteira roda numa transacao, e isso NAO e zelo: o cursor avanca por
// LOTE. Se metade de um lote ficasse gravada e o cursor nao avancasse, o
// reprocessamento seria inofensivo — mas se metade ficasse e o cursor avancasse, a
// outra metade sumiria para sempre. A transacao e o que torna as duas metades
// indistinguiveis para quem retoma.
//
// A violacao usada aqui e a REAL: um banco sem a migracao 0002 nao tem a coluna
// id_do_ponto_de_medicao, e o INSERT falha. Preferi isso a inventar um valor
// invalido — a falha que este teste precisa exercitar e a que de fato acontece em
// campo, e uma falha fabricada envelheceria assim que o banco mudasse de opiniao
// sobre ela.
func TestLoteFalhoNaoDeixaLinhaGravada(t *testing.T) {
	t.Parallel()

	projetor, pool, _ := bancoComMigracoes(t, 1)
	dispositivo := "camara-" + identificador.Sortear("")

	lote := []projetortimescale.LinhaProjetada{
		linhaDeTeste(dispositivo, 1),
		linhaDeTeste(dispositivo, 2),
	}

	if err := projetor.Projetar(t.Context(), lote); err == nil {
		t.Fatal("projecao contra esquema desatualizado deveria falhar")
	}

	var total int64
	if err := pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM leitura WHERE id_do_dispositivo = $1`, dispositivo).Scan(&total); err != nil {
		t.Fatalf("contagem falhou: %v", err)
	}
	if total != 0 {
		t.Fatalf("linhas = %d apos lote falho, esperado 0: a transacao nao reverteu", total)
	}
}
