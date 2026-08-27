package diariosqlite_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/saida/diariosqlite"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
)

// A hipotese que estes testes existem para confirmar ou derrubar: o gargalo da
// ingestao e o fsync do diario, e nao CPU nem serializacao.
//
// Ela vem do dimensionamento em papel — sem lote, o fsync sozinho consumiria a
// capacidade de um SSD no cenario alvo. Nunca foi medida. "Estimativa conservadora"
// nao e resposta defensavel quando alguem pergunta quantos dispositivos o sistema
// aguenta.

// diarioParaMedicao abre um diario em DISCO DE VERDADE.
//
// Isto e o cuidado mais importante deste arquivo, e ele custou uma medicao inteira
// jogada fora.
//
// b.TempDir() usa TMPDIR, e em muitos sistemas /tmp e tmpfs — memoria. Medindo la, o
// fsync nunca alcanca midia fisica, e a primeira rodada destes testes reportou
// ~8.000 envelopes/s com lote unitario. O numero e real e mede a coisa errada:
// descreve a RAM da maquina, nao a capacidade do diario numa planta.
//
// Publicar isso como "capacidade" seria pior que nao medir. Alguem dimensionaria uma
// instalacao com um numero que o disco nunca vai entregar.
//
// SYNKACORE_DISCO_DE_MEDICAO aponta para uma pasta em disco persistente. Sem ela, o
// teste PULA em vez de medir errado — recusar-se a responder e melhor que responder
// com confianca infundada.
func diarioParaMedicao(b *testing.B) *diariosqlite.Diario {
	b.Helper()

	pasta := os.Getenv("SYNKACORE_DISCO_DE_MEDICAO")
	if pasta == "" {
		b.Skip("defina SYNKACORE_DISCO_DE_MEDICAO com uma pasta em DISCO (nao tmpfs): " +
			"medir fsync em tmpfs descreve a RAM, nao o diario")
	}

	arquivo, err := os.MkdirTemp(pasta, "medicao-")
	if err != nil {
		b.Fatalf("nao foi possivel criar a pasta de medicao: %v", err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(arquivo) })

	diario, err := diariosqlite.Abrir(b.Context(), filepath.Join(arquivo, "medicao.db"))
	if err != nil {
		b.Fatalf("abertura falhou: %v", err)
	}
	b.Cleanup(func() { _ = diario.Fechar() })
	return diario
}

func loteParaMedicao(b *testing.B, sessao string, quantidade int, deslocamento uint64) []aquisicao.Envelope {
	b.Helper()

	lote := make([]aquisicao.Envelope, 0, quantidade)
	for indice := range quantidade {
		sequencia := deslocamento + uint64(indice) + 1
		lote = append(lote, envelopeParaMedicao(b, sessao, sequencia))
	}
	return lote
}

// BenchmarkGravarLote mede o custo por envelope conforme o tamanho do lote.
//
// E a medicao que decide se lote e otimizacao ou pre-requisito. Se o custo por
// envelope cair muito com o tamanho do lote, o gargalo e por TRANSACAO — o fsync — e
// nao por registro.
func BenchmarkGravarLote(b *testing.B) {
	tamanhos := map[string]int{
		"lote de 1":   1,
		"lote de 10":  10,
		"lote de 100": 100,
		"lote de 500": 500,
	}

	for nome, tamanho := range tamanhos {
		b.Run(nome, func(b *testing.B) {
			diario := diarioParaMedicao(b)

			// Lotes montados FORA da medicao: construir envelope custa validacao e
			// copia defensiva, e medir isso junto atribuiria ao disco um custo que e
			// de CPU. O que se quer medir aqui e a gravacao.
			lotes := make([][]aquisicao.Envelope, b.N)
			for indice := range b.N {
				lotes[indice] = loteParaMedicao(b, "boot-medicao", tamanho, uint64(indice*tamanho))
			}

			b.ResetTimer()
			b.ReportAllocs()

			for indice := range b.N {
				if _, err := diario.GravarLote(b.Context(), lotes[indice]); err != nil {
					b.Fatalf("gravacao falhou: %v", err)
				}
			}

			b.StopTimer()

			// Envelopes por segundo, que e a unidade em que a capacidade e discutida.
			// Transacoes por segundo esconderia o efeito do lote, que e justamente o
			// que este teste existe para expor.
			porSegundo := float64(tamanho) * float64(b.N) / b.Elapsed().Seconds()
			b.ReportMetric(porSegundo, "envelopes/s")
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*tamanho), "ns/envelope")
		})
	}
}

// BenchmarkNovoEnvelope mede o custo de validar e construir uma mensagem.
//
// Serve de contraponto: se ele for ordens de grandeza mais barato que a gravacao, a
// CPU nao e o gargalo e otimizar decodificacao seria trabalho no lugar errado.
func BenchmarkNovoEnvelope(b *testing.B) {
	catalogo, err := aquisicao.NovoCatalogoDeConteudo(aquisicao.TodasAsDefinicoes()...)
	if err != nil {
		b.Fatalf("catalogo falhou: %v", err)
	}
	parametros := parametrosParaMedicao(b)

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		if _, err := aquisicao.NovoEnvelope(parametros, catalogo); err != nil {
			b.Fatalf("construcao falhou: %v", err)
		}
	}
}

// BenchmarkLerAPartirDe mede o custo da leitura que a projecao faz a cada ciclo.
func BenchmarkLerAPartirDe(b *testing.B) {
	diario := diarioParaMedicao(b)

	const registrosGravados = 5_000
	if _, err := diario.GravarLote(b.Context(),
		loteParaMedicao(b, "boot-medicao", registrosGravados, 0)); err != nil {
		b.Fatalf("preparacao falhou: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for range b.N {
		if _, err := diario.LerAPartirDe(b.Context(), 0, 500); err != nil {
			b.Fatalf("leitura falhou: %v", err)
		}
	}
}
