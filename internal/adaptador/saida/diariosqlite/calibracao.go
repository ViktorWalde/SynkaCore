package diariosqlite

import (
	"context"
	"sort"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const (
	operacaoCalibrar = "diariosqlite.MedirCustoDeTransacao"

	// tamanhoDaCargaDeCalibracao aproxima um envelope tipico.
	//
	// O valor exato importa pouco: a V2.3 mediu que o custo e por TRANSACAO e nao
	// por registro. Escrever alguns bytes em vez de nada evita medir um commit
	// vazio, que o SQLite poderia otimizar para algo que a ingestao nunca alcanca.
	tamanhoDaCargaDeCalibracao = 64
)

// MedirCustoDeTransacao mede quanto custa confirmar uma transacao neste disco.
//
// PARA QUE ELA EXISTE. A portaria estima a espera multiplicando o custo medio de
// uma gravacao pelo tamanho da fila, e ate a V2.4 esse custo comecava em ZERO: sem
// nenhuma remessa concluida, a estimativa era zero, cabia em qualquer orcamento, e
// a portaria admitia todo mundo.
//
// A degradacao era segura e valia exatamente no pior momento. Depois de um
// reinicio do gateway, a frota inteira reconecta ao mesmo tempo, cada origem com o
// buffer cheio — e e ai que a admissao nao tinha nada com que decidir.
//
// O QUE ELA MEDE, E O QUE ELA NAO MEDE. Ela mede o termo FIXO: uma transacao de uma
// linha, que e essencialmente o fsync. Uma remessa real paga isso mais o trabalho
// por registro, entao o numero devolvido aqui e um PISO, nunca uma previsao.
//
// Piso e a direcao certa do erro. Semear a portaria com um valor abaixo do real faz
// ela admitir um pouco mais que o ideal nas primeiras remessas, e jamais recusar
// alguem que caberia — enquanto a media movel corrige para cima em poucas
// gravacoes de verdade. Errar para o lado de admitir e o erro barato: recusar por
// engano custaria retransmissao de dado bom.
//
// MEDIANA, e nao media. A calibracao roda na PARTIDA, que e quando a maquina
// inteira esta subindo: um pico de I/O de outro servico contamina uma amostra e
// arrastaria a media para cima, fazendo o gateway nascer recusando. A mediana
// ignora o pico sem precisar decidir o que e outlier.
func (d *Diario) MedirCustoDeTransacao(ctx context.Context, amostras int) (time.Duration, error) {
	if amostras < 1 {
		return 0, falha.Nova(falha.CategoriaEntradaInvalida, operacaoCalibrar,
			"a calibracao exige ao menos uma amostra")
	}

	carga := make([]byte, tamanhoDaCargaDeCalibracao)

	// A limpeza acontece com contexto PROPRIO e roda mesmo se a medicao falhar no
	// meio. Sem isso, uma calibracao interrompida deixaria linhas para tras e a
	// proxima partida mediria um arquivo maior a cada reinicio.
	defer d.limparCalibracao()

	custos := make([]time.Duration, 0, amostras)
	for range amostras {
		if err := ctx.Err(); err != nil {
			return 0, falha.Envolver(falha.CategoriaIndisponivel, operacaoCalibrar,
				"calibracao interrompida", err)
		}

		// O relogio aqui e time.Now direto, e nao o relogio injetado. E deliberado:
		// isto mede uma DURACAO curta dentro de uma unica chamada, e o que se quer e
		// a leitura monotonica que time.Since preserva. Um relogio falso nao teria o
		// que medir — nao ha nada a simular numa gravacao de verdade.
		inicio := time.Now()

		transacao, err := d.banco.BeginTx(ctx, nil)
		if err != nil {
			return 0, falha.Envolver(falha.CategoriaInterna, operacaoCalibrar,
				"nao foi possivel iniciar a transacao de calibracao", err)
		}
		if _, err := transacao.ExecContext(ctx,
			`INSERT INTO calibracao_de_disco (carga) VALUES (?)`, carga); err != nil {
			_ = transacao.Rollback()
			return 0, falha.Envolver(falha.CategoriaInterna, operacaoCalibrar,
				"falha ao gravar a linha de calibracao", err)
		}
		if err := transacao.Commit(); err != nil {
			return 0, falha.Envolver(falha.CategoriaInterna, operacaoCalibrar,
				"falha ao confirmar a transacao de calibracao", err)
		}

		custos = append(custos, time.Since(inicio))
	}

	sort.Slice(custos, func(primeiro, segundo int) bool {
		return custos[primeiro] < custos[segundo]
	})
	return custos[len(custos)/2], nil
}

// limparCalibracao apaga o que a medicao escreveu.
//
// Contexto proprio e curto porque isto roda tambem no caminho de erro, quando o
// contexto do chamador pode ja estar cancelado — e deixar lixo para tras seria pior
// que a falha que causou o cancelamento.
func (d *Diario) limparCalibracao() {
	ctx, cancelar := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelar()

	_, _ = d.banco.ExecContext(ctx, `DELETE FROM calibracao_de_disco`)
}

// ContarCalibracoesPendentes devolve quantas linhas de calibracao restaram.
//
// Existe para o teste que trava a limpeza. Sem ele, a verificacao teria de abrir o
// arquivo SQLite por fora e conhecer o nome da tabela — acoplando o teste ao
// esquema por um caminho que o resto do sistema nao usa, e que ficaria desatualizado
// em silencio na primeira vez que o esquema mudasse.
func (d *Diario) ContarCalibracoesPendentes(ctx context.Context) (int64, error) {
	var total int64
	if err := d.banco.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calibracao_de_disco`).Scan(&total); err != nil {
		return 0, falha.Envolver(falha.CategoriaInterna, operacaoCalibrar,
			"falha ao contar as linhas de calibracao", err)
	}
	return total, nil
}
