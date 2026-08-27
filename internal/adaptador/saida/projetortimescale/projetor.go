// Package projetortimescale escreve o modelo de leitura no TimescaleDB.
//
// E o estagio de CONSULTA do sistema, e nunca o de registro. O registro
// autoritativo e o diario local; esta tabela e uma projecao dele e pode ser
// reconstruida inteira a partir dali.
//
// A consequencia dessa separacao e a propriedade central da V2.0: a queda deste
// banco NAO ameaca dado nenhum. Na V1.x ela ameacava, e por isso havia um caminho
// de excecao com buffer de emergencia — um caminho que quase nunca rodava e que a
// auditoria da V1.2 encontrou desligado por um erro de tipo no construtor. Aqui
// nao ha caminho de excecao a manter funcionando: se este banco cai, a projecao
// para, o diario continua, e a retomada e o mesmo codigo de sempre.
package projetortimescale

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const (
	operacaoAbrir     = "projetortimescale.Abrir"
	operacaoProjetar  = "projetortimescale.Projetar"
	operacaoVerificar = "projetortimescale.Verificar"
)

// LinhaProjetada e um campo de um envelope, pronto para o modelo de leitura.
//
// Uma linha por CAMPO, e nao por envelope: e o formato estreito que a serie
// temporal comprime bem e que o dashboard consulta sem precisar conhecer a forma
// interna de cada tipo de conteudo.
type LinhaProjetada struct {
	InstanteObservado time.Time

	// InstanteEstimado e nulo quando a sessao ainda nao tem ancora, ou quando a
	// ancora foi invalidada por degrau de relogio. Nulo e informacao: significa "o
	// gateway nao pode afirmar quando isto foi medido", e e melhor que um valor
	// inventado que pareceria uma medicao.
	InstanteEstimado *time.Time

	TempoLigadoMs     int64
	IDDoDispositivo   string
	IDDaSessaoDeBoot  string
	NumeroDeSequencia int64
	TipoDeConteudo    string
	ClasseDeDado      string

	NomeDoCampo   string
	ValorNumerico *float64
	ValorTexto    *string
	ValorLogico   *bool
}

// Projetor grava o modelo de leitura.
type Projetor struct {
	pool *pgxpool.Pool
}

// Abrir conecta ao banco de consulta.
//
// NAO aplica migracao. O esquema vive em migracoes/ e e aplicado na instalacao,
// deliberadamente fora do caminho de execucao: um servico que altera o proprio
// esquema na partida transforma um rollback de binario numa migracao reversa
// nao planejada, e isso numa planta a 400 km de distancia, num sabado.
func Abrir(ctx context.Context, urlDeConexao string) (*Projetor, error) {
	pool, err := pgxpool.New(ctx, urlDeConexao)
	if err != nil {
		return nil, falha.Envolver(falha.CategoriaIndisponivel, operacaoAbrir,
			"nao foi possivel abrir o banco de consulta", err)
	}
	return &Projetor{pool: pool}, nil
}

// Fechar libera as conexoes.
func (p *Projetor) Fechar() { p.pool.Close() }

// Projetar grava um lote de linhas numa unica transacao.
//
// IDEMPOTENTE por construcao: ON CONFLICT DO NOTHING sobre a restricao de
// unicidade do modelo de leitura. Isso e o que permite ao cursor de projecao
// avancar DEPOIS da gravacao — se o gateway cair entre as duas coisas, o intervalo
// e reprocessado e nada duplica.
//
// A ordem inversa (avancar o cursor primeiro) perderia o intervalo para sempre. A
// escolha e entre "as vezes refaz trabalho" e "as vezes perde dado", e ela nao e
// dificil.
func (p *Projetor) Projetar(ctx context.Context, linhas []LinhaProjetada) error {
	if len(linhas) == 0 {
		return nil
	}

	transacao, err := p.pool.Begin(ctx)
	if err != nil {
		return falha.Envolver(falha.CategoriaIndisponivel, operacaoProjetar,
			"nao foi possivel iniciar a transacao de projecao", err)
	}
	defer func() { _ = transacao.Rollback(ctx) }()

	lote := &pgx.Batch{}
	for _, linha := range linhas {
		lote.Queue(`
			INSERT INTO leitura (
				instante_observado, instante_estimado, tempo_ligado_ms,
				id_do_dispositivo, id_da_sessao_de_boot, numero_de_sequencia,
				tipo_de_conteudo, classe_de_dado,
				nome_do_campo, valor_numerico, valor_texto, valor_logico
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT ON CONSTRAINT leitura_unica DO NOTHING`,
			linha.InstanteObservado, linha.InstanteEstimado, linha.TempoLigadoMs,
			linha.IDDoDispositivo, linha.IDDaSessaoDeBoot, linha.NumeroDeSequencia,
			linha.TipoDeConteudo, linha.ClasseDeDado,
			linha.NomeDoCampo, linha.ValorNumerico, linha.ValorTexto, linha.ValorLogico)
	}

	resultados := transacao.SendBatch(ctx, lote)
	for range linhas {
		if _, err := resultados.Exec(); err != nil {
			_ = resultados.Close()
			return falha.Envolver(falha.CategoriaIndisponivel, operacaoProjetar,
				"falha ao gravar linha no modelo de leitura", err)
		}
	}
	if err := resultados.Close(); err != nil {
		return falha.Envolver(falha.CategoriaIndisponivel, operacaoProjetar,
			"falha ao encerrar o lote de projecao", err)
	}

	if err := transacao.Commit(ctx); err != nil {
		return falha.Envolver(falha.CategoriaIndisponivel, operacaoProjetar,
			"falha ao confirmar a transacao de projecao", err)
	}
	return nil
}

// Verificar confirma que o banco de consulta responde.
//
// Executa consulta de verdade, e nao um Ping: uma conexao viva contra um banco sem
// o esquema aplicado responde ao ping e falha em toda gravacao. Relatar saudavel
// nesse estado seria enganar quem esta de plantao — a mesma regra que o health
// check do diario ja obedece.
func (p *Projetor) Verificar(ctx context.Context) error {
	var existe bool
	err := p.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'leitura')`).Scan(&existe)
	if err != nil {
		return falha.Envolver(falha.CategoriaIndisponivel, operacaoVerificar,
			"o banco de consulta nao respondeu", err)
	}
	if !existe {
		return falha.Nova(falha.CategoriaIndisponivel, operacaoVerificar,
			"o banco de consulta responde mas o modelo de leitura nao esta aplicado: rode as migracoes")
	}
	return nil
}

// LinhasDe converte um envelope decodificado nas linhas do modelo de leitura.
//
// Um PROJETOR GENERICO, e nao um por tipo de conteudo. Ele nao conhece tipo nenhum:
// cada conteudo declara o que contribui ao modelo de leitura em CamposProjetados, e
// acrescentar um tipo novo nao toca nesta funcao.
//
// A alternativa — um projetor por tipo — seria exatamente a duplicacao que o
// projeto proibe, e a divergencia apareceria como nomes de coluna diferentes para o
// mesmo conceito, descobertos meses depois por quem monta o dashboard.
func LinhasDe(conteudo aquisicao.ConteudoDecodificado, base LinhaProjetada) []LinhaProjetada {
	campos := conteudo.CamposProjetados()
	linhas := make([]LinhaProjetada, 0, len(campos))

	for _, campo := range campos {
		linha := base
		linha.NomeDoCampo = campo.Nome

		// Sem clausula default: o linter exhaustive cobra um caso novo aqui se
		// ValorProjetado ganhar um tipo. Como a interface e SELADA, isso so pode
		// acontecer deliberadamente — e deve quebrar o build junto com a coluna que
		// o esquema precisaria ganhar.
		switch valor := campo.Valor.(type) {
		case aquisicao.ValorNumerico:
			numerico := float64(valor)
			linha.ValorNumerico = &numerico
		case aquisicao.ValorTexto:
			texto := string(valor)
			linha.ValorTexto = &texto
		case aquisicao.ValorLogico:
			logico := bool(valor)
			linha.ValorLogico = &logico
		}

		linhas = append(linhas, linha)
	}
	return linhas
}
