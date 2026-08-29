// Package diariosqlite implementa o diario de ingestao — a definicao de
// durabilidade do SynkaCore.
//
// A inversao mais importante em relacao a V1.x: o diario NAO e um plano B para
// quando o banco de consulta cai. Ele e o caminho de escrita PRIMARIO e unico.
// Toda remessa aceita e gravada aqui antes de qualquer confirmacao a origem, e um
// projetor assincrono alimenta o banco de consulta a partir daqui.
//
// A diferenca e estrutural, nao de arranjo. Na V1.x, "zero perda" dependia de um
// caminho de excecao funcionar no pior momento — e a auditoria da V1.2 encontrou
// justamente isso: o worker recebia o tipo concreto em vez da interface, o buffer
// inteiro estava registrado e nunca era usado, e a perda de dado voltava a
// acontecer sem nada acusar. Um caminho de excecao que ninguem exercita e um
// caminho que nao funciona.
//
// Aqui nao ha caminho de excecao: se o diario falha, a remessa NAO e confirmada,
// e a origem retransmite. Zero perda deixa de ser promessa e vira consequencia de
// so existir um caminho.
//
// SQLite nao e abstraido atras de uma interface, de proposito. Nao havera um
// segundo diario: abstrair sem segunda implementacao e so indirecao, e o teste
// usa arquivo temporario, que e mais fiel que qualquer duble.
package diariosqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // driver SQLite em Go puro, sem cgo

	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const (
	operacaoAbrir            = "diariosqlite.Abrir"
	operacaoGravarLote       = "diariosqlite.GravarLote"
	operacaoLerNaoProjetados = "diariosqlite.LerNaoProjetados"
	operacaoAvancarCursor    = "diariosqlite.AvancarCursor"
	operacaoLerCursor        = "diariosqlite.LerCursor"
	operacaoPodar            = "diariosqlite.Podar"
	operacaoContar           = "diariosqlite.Contar"
	operacaoOcupacao         = "diariosqlite.Ocupacao"

	// formatoDeInstante e a UNICA serializacao de instante do diario.
	//
	// RFC 3339 com nanossegundos, sempre em UTC. Independente de locale por
	// natureza — que era um risco real e ja identificado: gravar um instante
	// formatado pelo locale da maquina produz texto que a leitura nao reconhece,
	// e a falha e silenciosa.
	formatoDeInstante = time.RFC3339Nano
)

// Diario e o diario de ingestao sobre um arquivo SQLite.
type Diario struct {
	banco *sql.DB

	// caminho e o arquivo do diario, guardado para medir a ocupacao.
	//
	// O tamanho vem do SISTEMA DE ARQUIVOS, e nao de `PRAGMA page_count`. A pergunta
	// que importa nao e quantas paginas o SQLite alocou, e sim quanto DISCO o diario
	// esta ocupando — e isso inclui o WAL, que entre checkpoints pode ser fracao
	// significativa do total.
	caminho string

	// tetoDeBytes limita o quanto o diario pode ocupar. Zero desliga o teto.
	tetoDeBytes int64

	// bytesOcupados e a ultima medicao, e excedeuOTeto o veredito dela.
	//
	// ATOMICOS, alimentados por um laco periodico em vez de medidos a cada remessa.
	// Um os.Stat por remessa seria correto e desnecessario: mesmo a 20.000
	// envelopes/s o diario cresce alguns megabytes por segundo, e uma medicao a cada
	// dez segundos erra por uma margem que o proprio teto acomoda. O caminho quente
	// paga uma leitura atomica.
	bytesOcupados atomic.Int64
	excedeuOTeto  atomic.Bool
}

// Abrir cria ou abre o diario no caminho indicado e aplica o esquema.
//
// O caminho e de ARQUIVO, nunca em memoria em producao: um diario em memoria
// entrega desempenho e nenhuma durabilidade, que e o oposto do que este
// componente existe para dar.
func Abrir(ctx context.Context, caminho string) (*Diario, error) {
	banco, err := sql.Open("sqlite", caminho)
	if err != nil {
		return nil, falha.Envolver(falha.CategoriaInterna, operacaoAbrir,
			"nao foi possivel abrir o diario em "+caminho, err)
	}

	// UM escritor, e um so.
	//
	// SQLite permite exatamente um escritor por vez; com varias conexoes, o
	// excedente nao ganha paralelismo — ganha SQLITE_BUSY, que e um modo de falha
	// a tratar em vez de uma condicao a evitar. Serializar aqui elimina a classe
	// inteira de defeito, e o custo e nenhum na faixa dimensionada: com lote e
	// group commit, o diario faz dezenas de transacoes por segundo, nao milhares.
	//
	// Quando medicao real mostrar a projecao competindo com a ingestao, o passo e
	// separar a conexao de leitura — o WAL ja permite. Fazer isso agora seria
	// otimizar por intuicao um recurso que esta praticamente ocioso.
	banco.SetMaxOpenConns(1)
	banco.SetConnMaxLifetime(0)

	if _, err := banco.ExecContext(ctx, esquema); err != nil {
		_ = banco.Close()
		return nil, falha.Envolver(falha.CategoriaInterna, operacaoAbrir,
			"nao foi possivel aplicar o esquema do diario", err)
	}

	return &Diario{banco: banco, caminho: caminho}, nil
}

// ComTetoDeBytes limita o quanto o diario pode ocupar em disco.
//
// POR QUE O TETO EXISTE, e por que ele nao pode depender da poda: a poda so remove o
// que ja foi projetado E envelheceu, e portanto NAO REMOVE NADA quando nao ha
// projecao configurada — que e exatamente a configuracao recomendada para comissionar
// uma planta e para operar sem infraestrutura analitica. Um gateway deixado "so
// adquirindo" cresce sem limite ate encher o disco.
//
// E encher o disco e o pior desfecho disponivel. Recusar remessa e recuperavel: a
// origem preserva o lote e retransmite. Disco cheio nao e recuperavel pelo gateway —
// o SQLite passa a falhar, o log para de ser escrito, e a maquina inteira vai junto.
//
// Zero desliga o teto. Util em teste e em maquina com disco dedicado; em producao ele
// deve estar ligado.
func (d *Diario) ComTetoDeBytes(teto int64) *Diario {
	d.tetoDeBytes = teto
	return d
}

// sufixosDoArquivo sao os companheiros que o modo WAL cria ao lado do diario.
//
// Os tres somados sao o que de fato ocupa o disco. Medir so o .db subestimaria a
// ocupacao justamente sob carga, que e quando o WAL cresce entre checkpoints — e o
// teto erraria para o lado perigoso.
var sufixosDoArquivo = []string{"", "-wal", "-shm"}

// MedirOcupacao atualiza o tamanho observado do diario e o veredito do teto.
//
// Chamada por um laco periodico, nunca no caminho de gravacao.
func (d *Diario) MedirOcupacao() (int64, error) {
	var total int64
	for _, sufixo := range sufixosDoArquivo {
		informacao, err := os.Stat(d.caminho + sufixo)
		if err != nil {
			// Ausencia do WAL ou do SHM e NORMAL: eles nascem no primeiro uso e somem
			// no fechamento limpo. Apenas o arquivo principal faltando seria anomalia,
			// e ela aparece na proxima gravacao de qualquer jeito.
			if os.IsNotExist(err) {
				continue
			}
			return 0, falha.Envolver(falha.CategoriaInterna, operacaoOcupacao,
				"nao foi possivel medir a ocupacao do diario", err)
		}
		total += informacao.Size()
	}

	d.bytesOcupados.Store(total)
	d.excedeuOTeto.Store(d.tetoDeBytes > 0 && total >= d.tetoDeBytes)
	return total, nil
}

// Ocupacao devolve a ultima medicao e o teto configurado.
func (d *Diario) Ocupacao() (bytes, teto int64) {
	return d.bytesOcupados.Load(), d.tetoDeBytes
}

// Fechar libera o arquivo do diario.
func (d *Diario) Fechar() error { return d.banco.Close() }

// ResultadoDaGravacao relata o que aconteceu com um lote.
type ResultadoDaGravacao struct {
	// Gravados e quantos envelopes eram novos e entraram.
	Gravados int

	// Duplicados e quantos ja existiam e foram ignorados.
	//
	// NAO e anomalia e nao deve alarmar: reentrega e consequencia esperada de
	// entrega ao-menos-uma-vez. O numero existe para observabilidade — duplicacao
	// subindo indica que as confirmacoes nao estao chegando de volta a origem.
	Duplicados int

	// MaiorSequenciaDuravel e ate onde a origem pode liberar o buffer.
	MaiorSequenciaDuravel uint64
}

// GravarLote persiste um lote inteiro numa unica transacao.
//
// GROUP COMMIT e a razao de esta funcao receber um lote em vez de um envelope.
// Nao e otimizacao: sem ele, o fsync sozinho consumiria toda a capacidade do disco
// no cenario dimensionado, e nao sobraria nada para o banco de consulta nem para
// picos. Com lote, um fsync serve a remessa inteira.
//
// A transacao e tudo-ou-nada de proposito. Gravar metade e confirmar metade
// deixaria a origem sem saber o que reter, e "confirmado ate a sequencia N" e
// justamente a garantia que permite a ela liberar o buffer com seguranca.
func (d *Diario) GravarLote(ctx context.Context, envelopes []aquisicao.Envelope) (ResultadoDaGravacao, error) {
	if len(envelopes) == 0 {
		return ResultadoDaGravacao{}, nil
	}

	// O TETO E CONFERIDO ANTES DE ABRIR A TRANSACAO, e a recusa e a resposta certa.
	//
	// A origem preserva o lote e retransmite: o dado nao se perde, ele espera do outro
	// lado. E esperar do outro lado e infinitamente melhor que o gateway seguir
	// gravando ate o sistema de arquivos encher — a partir dali nao ha mais recusa
	// graciosa, ha uma maquina que parou.
	if d.excedeuOTeto.Load() {
		bytes, teto := d.Ocupacao()
		return ResultadoDaGravacao{}, falha.Nova(falha.CategoriaArmazenamentoEsgotado,
			operacaoGravarLote,
			"o diario alcancou o teto de "+formatarBytes(teto)+" (ocupando "+
				formatarBytes(bytes)+"). Ligue a projecao para que a poda libere espaco, "+
				"aumente o teto, ou libere disco")
	}

	transacao, err := d.banco.BeginTx(ctx, nil)
	if err != nil {
		return ResultadoDaGravacao{}, falha.Envolver(falha.CategoriaInterna, operacaoGravarLote,
			"nao foi possivel iniciar a transacao do diario", err)
	}
	defer func() { _ = transacao.Rollback() }()

	// ON CONFLICT DO NOTHING torna a reentrega um caso NORMAL, tratado pelo banco,
	// e nao um erro a interpretar no codigo. Consultar antes de inserir teria uma
	// janela de corrida entre a consulta e a insercao; a restricao de unicidade
	// nao tem.
	comando, err := transacao.PrepareContext(ctx, `
		INSERT INTO diario (
			chave_de_idempotencia, id_do_dispositivo, id_da_sessao_de_boot,
			numero_de_sequencia, versao_do_esquema, tipo_de_conteudo,
			classe_de_dado, tempo_ligado_ms, instante_observado, conteudo_bruto
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (chave_de_idempotencia) DO NOTHING`)
	if err != nil {
		return ResultadoDaGravacao{}, falha.Envolver(falha.CategoriaInterna, operacaoGravarLote,
			"nao foi possivel preparar a insercao no diario", err)
	}
	defer func() { _ = comando.Close() }()

	var resultado ResultadoDaGravacao
	for _, envelope := range envelopes {
		chave := envelope.ChaveDeIdempotencia()

		saida, err := comando.ExecContext(ctx,
			chave.String(),
			chave.IDDoDispositivo().String(),
			chave.IDDaSessaoDeBoot().String(),
			int64(chave.NumeroDeSequencia()),
			int64(envelope.VersaoDoEsquema()),
			string(envelope.Tipo()),
			envelope.ClasseDeDado().String(),
			envelope.TempoLigado().Milliseconds(),
			envelope.InstanteObservado().UTC().Format(formatoDeInstante),
			envelope.ConteudoBruto(),
		)
		if err != nil {
			// Falha do diario e CategoriaInterna, nunca Indisponivel: se o diario
			// falha, a durabilidade — que e a propriedade que este sistema existe
			// para entregar — foi violada. Nao ha degradacao graciosa possivel.
			return ResultadoDaGravacao{}, falha.Envolver(falha.CategoriaInterna, operacaoGravarLote,
				"falha ao gravar envelope no diario", err)
		}

		afetadas, err := saida.RowsAffected()
		if err != nil {
			return ResultadoDaGravacao{}, falha.Envolver(falha.CategoriaInterna, operacaoGravarLote,
				"falha ao contabilizar a gravacao no diario", err)
		}
		if afetadas == 0 {
			resultado.Duplicados++
		} else {
			resultado.Gravados++
		}

		// Duplicata conta como duravel. Ela ja esta no diario — recusar avancar a
		// confirmacao faria a origem retransmitir para sempre o que ja esta salvo.
		if numero := chave.NumeroDeSequencia(); numero > resultado.MaiorSequenciaDuravel {
			resultado.MaiorSequenciaDuravel = numero
		}
	}

	if err := transacao.Commit(); err != nil {
		return ResultadoDaGravacao{}, falha.Envolver(falha.CategoriaInterna, operacaoGravarLote,
			"falha ao confirmar a transacao do diario", err)
	}
	return resultado, nil
}

// RegistroDoDiario e uma linha lida do diario, na forma em que a projecao a consome.
type RegistroDoDiario struct {
	ID                  int64
	ChaveDeIdempotencia string
	IDDoDispositivo     string
	IDDaSessaoDeBoot    string
	NumeroDeSequencia   uint64
	VersaoDoEsquema     uint16
	TipoDeConteudo      string
	ClasseDeDado        string
	TempoLigado         time.Duration
	InstanteObservado   time.Time
	ConteudoBruto       []byte
}

// LerAPartirDe devolve ate `limite` registros com id maior que `ultimoID`, em
// ordem de gravacao.
//
// A ordem por id e a ordem de DURABILIDADE, que e a unica ordem total do sistema.
// Ordenar por instante observado seria errado: duas remessas podem chegar com o
// mesmo carimbo, e a ordem entre elas ficaria indefinida — e uma projecao que
// muda de ordem entre execucoes nao e reprocessavel.
func (d *Diario) LerAPartirDe(ctx context.Context, ultimoID int64, limite int) ([]RegistroDoDiario, error) {
	linhas, err := d.banco.QueryContext(ctx, `
		SELECT id, chave_de_idempotencia, id_do_dispositivo, id_da_sessao_de_boot,
		       numero_de_sequencia, versao_do_esquema, tipo_de_conteudo,
		       classe_de_dado, tempo_ligado_ms, instante_observado, conteudo_bruto
		  FROM diario
		 WHERE id > ?
		 ORDER BY id
		 LIMIT ?`, ultimoID, limite)
	if err != nil {
		return nil, falha.Envolver(falha.CategoriaInterna, operacaoLerNaoProjetados,
			"falha ao ler o diario", err)
	}
	defer func() { _ = linhas.Close() }()

	var registros []RegistroDoDiario
	for linhas.Next() {
		var registro RegistroDoDiario
		var sequencia, tempoLigadoMs int64
		var instante string

		if err := linhas.Scan(&registro.ID, &registro.ChaveDeIdempotencia,
			&registro.IDDoDispositivo, &registro.IDDaSessaoDeBoot,
			&sequencia, &registro.VersaoDoEsquema, &registro.TipoDeConteudo,
			&registro.ClasseDeDado, &tempoLigadoMs, &instante, &registro.ConteudoBruto); err != nil {
			return nil, falha.Envolver(falha.CategoriaInterna, operacaoLerNaoProjetados,
				"falha ao mapear linha do diario", err)
		}

		registro.NumeroDeSequencia = uint64(sequencia)
		registro.TempoLigado = time.Duration(tempoLigadoMs) * time.Millisecond

		registro.InstanteObservado, err = time.Parse(formatoDeInstante, instante)
		if err != nil {
			return nil, falha.Envolver(falha.CategoriaInterna, operacaoLerNaoProjetados,
				"instante observado ilegivel no diario: registro "+registro.ChaveDeIdempotencia, err)
		}

		registros = append(registros, registro)
	}
	if err := linhas.Err(); err != nil {
		return nil, falha.Envolver(falha.CategoriaInterna, operacaoLerNaoProjetados,
			"falha ao percorrer o diario", err)
	}
	return registros, nil
}

// LerCursor devolve ate onde o consumidor indicado ja projetou.
//
// Cursor ausente devolve zero, que e o comportamento correto na primeira partida:
// projetar tudo desde o inicio. Ele NAO e erro — tratar "ainda nao comecou" como
// falha obrigaria todo chamador a distinguir dois casos que tem a mesma resposta.
func (d *Diario) LerCursor(ctx context.Context, nome string) (int64, error) {
	var ultimoID int64
	err := d.banco.QueryRowContext(ctx,
		`SELECT ultimo_id_projetado FROM cursor_de_projecao WHERE nome = ?`, nome).Scan(&ultimoID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, falha.Envolver(falha.CategoriaInterna, operacaoLerCursor,
			"falha ao ler o cursor de projecao "+nome, err)
	}
	return ultimoID, nil
}

// AvancarCursor grava ate onde o consumidor projetou.
//
// Chamado DEPOIS de a projecao estar confirmada no destino, nunca antes. A ordem
// importa: avancar primeiro e falhar depois perderia o intervalo para sempre;
// projetar primeiro e falhar ao avancar apenas reprocessa, e reprocessar e
// idempotente por construcao.
func (d *Diario) AvancarCursor(ctx context.Context, nome string, ultimoID int64, agora time.Time) error {
	_, err := d.banco.ExecContext(ctx, `
		INSERT INTO cursor_de_projecao (nome, ultimo_id_projetado, atualizado_em)
		VALUES (?, ?, ?)
		ON CONFLICT (nome) DO UPDATE SET
			ultimo_id_projetado = excluded.ultimo_id_projetado,
			atualizado_em       = excluded.atualizado_em`,
		nome, ultimoID, agora.UTC().Format(formatoDeInstante))
	if err != nil {
		return falha.Envolver(falha.CategoriaInterna, operacaoAvancarCursor,
			"falha ao avancar o cursor de projecao "+nome, err)
	}
	return nil
}

// MaiorSequenciaDuravel devolve ate onde uma sessao de boot esta gravada.
//
// Serve a confirmacao apos reinicio do gateway: a origem retransmite o que nao foi
// confirmado, e esta consulta responde o que ja esta salvo sem precisar regravar.
func (d *Diario) MaiorSequenciaDuravel(ctx context.Context, dispositivo, sessao string) (uint64, error) {
	var maior sql.NullInt64
	err := d.banco.QueryRowContext(ctx, `
		SELECT MAX(numero_de_sequencia) FROM diario
		 WHERE id_do_dispositivo = ? AND id_da_sessao_de_boot = ?`, dispositivo, sessao).Scan(&maior)
	if err != nil {
		return 0, falha.Envolver(falha.CategoriaInterna, operacaoContar,
			"falha ao consultar a maior sequencia duravel", err)
	}
	if !maior.Valid {
		return 0, nil
	}
	return uint64(maior.Int64), nil
}

// Podar remove registros ja projetados por TODOS os cursores e mais antigos que a
// idade indicada.
//
// Existe porque append-only sem poda cresce sem limite — um defeito conhecido do
// desenho anterior, onde o arquivo de buffer chegaria a gigabytes ao longo de
// meses sem nenhuma politica que o contivesse.
//
// As duas condicoes sao necessarias JUNTAS. So a idade apagaria dado que a
// projecao ainda nao consumiu, durante uma parada prolongada do banco de consulta
// — exatamente o cenario em que o diario e a unica copia que existe. So o cursor
// apagaria o dado assim que projetado, e ai um erro descoberto na projecao seria
// irrecuperavel.
//
// Devolve quantos registros saíram.
func (d *Diario) Podar(ctx context.Context, idadeMinima time.Duration, agora time.Time) (int64, error) {
	// Se nao ha cursor nenhum, nao ha nada seguro para podar: significa que
	// ninguem projetou ainda, e apagar seria destruir a unica copia.
	var menorCursor sql.NullInt64
	if err := d.banco.QueryRowContext(ctx,
		`SELECT MIN(ultimo_id_projetado) FROM cursor_de_projecao`).Scan(&menorCursor); err != nil {
		return 0, falha.Envolver(falha.CategoriaInterna, operacaoPodar,
			"falha ao consultar o menor cursor de projecao", err)
	}
	if !menorCursor.Valid || menorCursor.Int64 == 0 {
		return 0, nil
	}

	limite := agora.UTC().Add(-idadeMinima).Format(formatoDeInstante)
	saida, err := d.banco.ExecContext(ctx,
		`DELETE FROM diario WHERE id <= ? AND instante_observado < ?`,
		menorCursor.Int64, limite)
	if err != nil {
		return 0, falha.Envolver(falha.CategoriaInterna, operacaoPodar,
			"falha ao podar o diario", err)
	}

	removidos, err := saida.RowsAffected()
	if err != nil {
		return 0, falha.Envolver(falha.CategoriaInterna, operacaoPodar,
			"falha ao contabilizar a poda do diario", err)
	}
	return removidos, nil
}

// Verificar confirma que o diario responde. Usado pelo health check.
//
// Executa uma consulta de verdade, e nao apenas Ping: uma conexao aberta sobre um
// arquivo corrompido responde ao ping e falha na leitura. Relatar saudavel quando
// o sistema nao esta e ativamente enganoso para quem esta de plantao.
func (d *Diario) Verificar(ctx context.Context) error {
	var total int64
	if err := d.banco.QueryRowContext(ctx, `SELECT COUNT(*) FROM diario`).Scan(&total); err != nil {
		return falha.Envolver(falha.CategoriaInterna, operacaoContar,
			"o diario nao respondeu a verificacao", err)
	}
	return d.verificarEscrita(ctx)
}

// verificarEscrita prova que o diario ainda ACEITA gravacao.
//
// A VERIFICACAO ANTERIOR SO LIA. Num disco cheio o SELECT continua funcionando
// enquanto todo INSERT falha, e o gateway reportaria `journal: available` recusando
// toda remessa — violando a regra que o proprio projeto escreveu, de que relatar
// saudavel quando o sistema nao esta e ativamente enganoso para quem esta de plantao.
// E violando-a no modo de falha MAIS PROVAVEL em campo.
//
// A sonda escreve na tabela de calibracao, e nao no diario, pela mesma razao que a
// calibracao de partida usa: uma linha fabricada que escapasse para o diario seria
// projetada ao modelo de leitura como se fosse medicao.
//
// INSERT e DELETE na MESMA transacao. Um unico commit — portanto um unico fsync, que
// e o que de fato prova a escrita — e nenhuma linha residual. Duas transacoes
// custariam dois fsync e deixariam residuo entre elas.
//
// O custo e um fsync por consulta de saude, na faixa de centenas de microssegundos.
// Ele disputa a conexao unica com a ingestao, e sob saturacao a consulta de saude
// entra na fila — o que e informacao verdadeira, e nao defeito: um /saude que responde
// rapido enquanto a ingestao esta presa descreveria um gateway que nao existe.
func (d *Diario) verificarEscrita(ctx context.Context) error {
	transacao, err := d.banco.BeginTx(ctx, nil)
	if err != nil {
		return falha.Envolver(falha.CategoriaInterna, operacaoContar,
			"o diario nao aceitou iniciar uma transacao de verificacao", err)
	}
	defer func() { _ = transacao.Rollback() }()

	resultado, err := transacao.ExecContext(ctx,
		`INSERT INTO calibracao_de_disco (carga) VALUES (?)`, []byte("saude"))
	if err != nil {
		return falha.Envolver(falha.CategoriaInterna, operacaoContar,
			"o diario nao aceita gravacao: o disco pode estar cheio ou somente leitura", err)
	}

	id, err := resultado.LastInsertId()
	if err != nil {
		return falha.Envolver(falha.CategoriaInterna, operacaoContar,
			"o diario nao confirmou a gravacao de verificacao", err)
	}
	if _, err := transacao.ExecContext(ctx,
		`DELETE FROM calibracao_de_disco WHERE id = ?`, id); err != nil {
		return falha.Envolver(falha.CategoriaInterna, operacaoContar,
			"o diario nao aceitou remover a linha de verificacao", err)
	}

	// O COMMIT e a prova. Ele forca o fsync que uma transacao revertida nao forcaria —
	// e sem fsync a verificacao passaria num disco que ja nao aceita escrita.
	if err := transacao.Commit(); err != nil {
		return falha.Envolver(falha.CategoriaInterna, operacaoContar,
			"o diario nao confirmou a transacao de verificacao: o disco pode estar cheio", err)
	}
	return nil
}

// formatarBytes devolve o tamanho em unidade legivel, para mensagem de operador.
//
// Existe porque a mensagem e lida por quem opera a planta, e "2147483648" nao diz
// nada que "2.0 GiB" nao diga melhor. Binario e nao decimal, para casar com o que
// `df` e `ls -lh` mostram na mesma maquina.
func formatarBytes(bytes int64) string {
	const unidade = 1024
	if bytes < unidade {
		return strconv.FormatInt(bytes, 10) + " B"
	}
	divisor, expoente := int64(unidade), 0
	for n := bytes / unidade; n >= unidade; n /= unidade {
		divisor *= unidade
		expoente++
	}
	return strconv.FormatFloat(float64(bytes)/float64(divisor), 'f', 1, 64) +
		" " + string("KMGT"[expoente]) + "iB"
}
