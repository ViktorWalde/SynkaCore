package diariosqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/autoridadedetempo"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const (
	operacaoLerAncora    = "diariosqlite.LerAncora"
	operacaoGravarAncora = "diariosqlite.GravarAncora"
)

// AncoraPersistida e uma ancora recuperada do disco.
//
// Carrega o identificador da execucao que a criou porque a leitura monotonica
// so e comparavel dentro do processo que a produziu. Quem consome precisa saber
// disso para nao interpretar uma troca de processo como degrau de relogio.
type AncoraPersistida struct {
	Ancora autoridadedetempo.AncoraDeSessaoDeBoot

	// DaExecucaoAtual informa se a leitura monotonica guardada foi tomada por
	// ESTE processo. Falso significa que a verificacao de degrau de relogio nao se
	// aplica a esta ancora ate que ela seja renovada.
	DaExecucaoAtual bool
}

// LerAncora recupera a ancora de uma sessao de boot, se ela existir.
//
// Ausencia NAO e erro: e o caso normal da primeira mensagem de uma sessao nova.
// O segundo retorno distingue "nao existe" de "existe", sem obrigar o chamador a
// interpretar um erro para descobrir isso.
func (d *Diario) LerAncora(ctx context.Context, dispositivo identidadededispositivo.IDDoDispositivo,
	sessao identidadededispositivo.IDDaSessaoDeBoot, idDaExecucaoAtual string) (AncoraPersistida, bool, error) {

	var (
		tempoLigadoMs int64
		instante      string
		decorridoNs   int64
		idDaExecucao  string
	)
	err := d.banco.QueryRowContext(ctx, `
		SELECT tempo_ligado_da_ancora_ms, instante_da_ancora, decorrido_da_ancora_ns, id_da_execucao
		  FROM ancora_de_sessao_de_boot
		 WHERE id_do_dispositivo = ? AND id_da_sessao_de_boot = ?`,
		dispositivo.String(), sessao.String(),
	).Scan(&tempoLigadoMs, &instante, &decorridoNs, &idDaExecucao)

	if errors.Is(err, sql.ErrNoRows) {
		return AncoraPersistida{}, false, nil
	}
	if err != nil {
		return AncoraPersistida{}, false, falha.Envolver(falha.CategoriaInterna, operacaoLerAncora,
			"falha ao ler a ancora de sessao de boot", err)
	}

	instanteDaAncora, err := time.Parse(formatoDeInstante, instante)
	if err != nil {
		return AncoraPersistida{}, false, falha.Envolver(falha.CategoriaInterna, operacaoLerAncora,
			"instante de ancoragem ilegivel no diario", err)
	}

	ancora, err := autoridadedetempo.NovaAncoraDeSessaoDeBoot(dispositivo, sessao,
		time.Duration(tempoLigadoMs)*time.Millisecond, instanteDaAncora,
		time.Duration(decorridoNs))
	if err != nil {
		return AncoraPersistida{}, false, falha.Envolver(falha.CategoriaInterna, operacaoLerAncora,
			"ancora persistida nao reconstroi um valor valido", err)
	}

	return AncoraPersistida{
		Ancora:          ancora,
		DaExecucaoAtual: idDaExecucao == idDaExecucaoAtual,
	}, true, nil
}

// GravarAncora persiste a ancora de uma sessao de boot, se ainda nao houver uma.
//
// DO NOTHING em conflito, e nao uma atualizacao, e a regra central deste tipo: a
// ancora e criada na PRIMEIRA mensagem de cada sessao e imutavel depois.
// Reancorar a cada mensagem faria a latencia de rede variavel contaminar toda a
// serie, deslocando amostras para frente e para tras sem relacao nenhuma com a
// realidade fisica.
//
// A escrita e condicional no banco, e nao verificada antes em Go, para fechar a
// corrida entre duas remessas simultaneas da mesma sessao: sem isso, as duas
// leriam "nao existe" e a segunda sobrescreveria a ancora da primeira.
func (d *Diario) GravarAncora(ctx context.Context, ancora autoridadedetempo.AncoraDeSessaoDeBoot,
	idDaExecucao string, agora time.Time) error {

	_, err := d.banco.ExecContext(ctx, `
		INSERT INTO ancora_de_sessao_de_boot (
			id_do_dispositivo, id_da_sessao_de_boot, tempo_ligado_da_ancora_ms,
			instante_da_ancora, decorrido_da_ancora_ns, id_da_execucao, criada_em
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id_do_dispositivo, id_da_sessao_de_boot) DO NOTHING`,
		ancora.IDDoDispositivo().String(),
		ancora.IDDaSessaoDeBoot().String(),
		ancora.TempoLigadoDaAncora().Milliseconds(),
		ancora.InstanteDaAncora().UTC().Format(formatoDeInstante),
		int64(ancora.DecorridoDaAncora()),
		idDaExecucao,
		agora.UTC().Format(formatoDeInstante),
	)
	if err != nil {
		return falha.Envolver(falha.CategoriaInterna, operacaoGravarAncora,
			"falha ao gravar a ancora de sessao de boot", err)
	}
	return nil
}
