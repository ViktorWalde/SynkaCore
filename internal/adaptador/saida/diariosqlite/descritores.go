package diariosqlite

import (
	"context"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const operacaoUltimosDescritores = "diariosqlite.UltimosDescritores"

// DescritorGravado e a ultima autodeclaracao de uma origem, lida do diario.
type DescritorGravado struct {
	IDDoDispositivo   string
	ConteudoBruto     []byte
	InstanteObservado time.Time
}

// UltimosDescritores devolve o descritor mais recente de cada dispositivo.
//
// Existe para reconstruir o relatorio de comissionamento na PARTIDA. Sem isso, o
// relatorio nasce vazio a cada reinicio do gateway e so se preenche quando cada
// origem reenvia — e ate la "nenhuma divergencia" seria facilmente confundido com
// "esta tudo certo", que e a conclusao errada.
//
// Le do proprio diario em vez de manter uma tabela de declaracoes. Guardar de novo
// o que ja esta gravado seria uma segunda copia do mesmo fato, e duas copias
// divergem — a que ninguem lembra de atualizar e sempre a secundaria.
//
// Consequencia aceita: o que a poda remove some tambem daqui. Como a poda so
// alcanca o que ja foi projetado E envelheceu alem da retencao, e como as origens
// reenviam descritor periodicamente, o caso em que isso morde e um dispositivo que
// nao fala ha mais tempo que a janela de retencao — e sobre esse dispositivo o
// gateway realmente nao tem o que afirmar.
func (d *Diario) UltimosDescritores(ctx context.Context, tipoDeConteudo string) ([]DescritorGravado, error) {
	linhas, err := d.banco.QueryContext(ctx, `
		SELECT id_do_dispositivo, conteudo_bruto, instante_observado
		  FROM diario
		 WHERE id IN (
		       SELECT MAX(id) FROM diario
		        WHERE tipo_de_conteudo = ?
		        GROUP BY id_do_dispositivo)
		 ORDER BY id_do_dispositivo`, tipoDeConteudo)
	if err != nil {
		return nil, falha.Envolver(falha.CategoriaInterna, operacaoUltimosDescritores,
			"falha ao consultar os ultimos descritores", err)
	}
	defer func() { _ = linhas.Close() }()

	var descritores []DescritorGravado
	for linhas.Next() {
		var descritor DescritorGravado
		var instante string

		if err := linhas.Scan(&descritor.IDDoDispositivo, &descritor.ConteudoBruto, &instante); err != nil {
			return nil, falha.Envolver(falha.CategoriaInterna, operacaoUltimosDescritores,
				"falha ao mapear descritor do diario", err)
		}
		descritor.InstanteObservado, err = time.Parse(formatoDeInstante, instante)
		if err != nil {
			return nil, falha.Envolver(falha.CategoriaInterna, operacaoUltimosDescritores,
				"instante observado ilegivel no diario", err)
		}
		descritores = append(descritores, descritor)
	}
	if err := linhas.Err(); err != nil {
		return nil, falha.Envolver(falha.CategoriaInterna, operacaoUltimosDescritores,
			"falha ao percorrer os descritores", err)
	}
	return descritores, nil
}
