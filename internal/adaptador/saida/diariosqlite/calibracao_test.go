package diariosqlite_test

import (
	"testing"
)

// TestCalibracaoMedeUmCustoPositivo trava o motivo de ela existir.
//
// A portaria nasce sem saber quanto custa gravar neste disco, e enquanto nao sabe a
// espera estimada e zero — cabe em qualquer orcamento e todo mundo entra. Uma
// calibracao que devolvesse zero deixaria exatamente esse estado de pe, com a
// aparencia de o ter fechado.
func TestCalibracaoMedeUmCustoPositivo(t *testing.T) {
	t.Parallel()

	diario := diarioDeTeste(t)

	custo, err := diario.MedirCustoDeTransacao(t.Context(), 5)
	if err != nil {
		t.Fatalf("calibracao falhou: %v", err)
	}
	if custo <= 0 {
		t.Fatalf("custo medido = %v, esperado positivo", custo)
	}
}

// TestCalibracaoNaoDeixaNadaNoDiario e a trava que justifica a tabela separada.
//
// Se a calibracao gravasse no proprio diario, uma queda no meio dela deixaria
// aquelas linhas para tras — e elas seriam PROJETADAS para o modelo de leitura como
// se fossem medicao. Dado fabricado com aparencia de observacao e o pior desfecho
// possivel num sistema cuja propriedade central e nunca mentir sobre o que viu.
func TestCalibracaoNaoDeixaNadaNoDiario(t *testing.T) {
	t.Parallel()

	diario := diarioDeTeste(t)

	if _, err := diario.MedirCustoDeTransacao(t.Context(), 5); err != nil {
		t.Fatalf("calibracao falhou: %v", err)
	}

	registros, err := diario.LerAPartirDe(t.Context(), 0, 100)
	if err != nil {
		t.Fatalf("leitura do diario falhou: %v", err)
	}
	if len(registros) != 0 {
		t.Fatalf("a calibracao deixou %d registros no diario", len(registros))
	}

	// E o diario continua utilizavel: a calibracao nao pode ter corrompido nada nem
	// deixado transacao aberta.
	if err := diario.Verificar(t.Context()); err != nil {
		t.Fatalf("o diario nao responde apos a calibracao: %v", err)
	}
}

// TestCalibracaoRepetidaNaoAcumula trava a limpeza entre partidas.
//
// Sem ela, cada reinicio do gateway deixaria linhas para tras e a partida seguinte
// mediria um arquivo maior — a calibracao piorando a si mesma a cada reinicio, que
// e o tipo de degradacao que so aparece meses depois numa planta que ninguem visita.
func TestCalibracaoRepetidaNaoAcumula(t *testing.T) {
	t.Parallel()

	diario := diarioDeTeste(t)

	for range 3 {
		if _, err := diario.MedirCustoDeTransacao(t.Context(), 5); err != nil {
			t.Fatalf("calibracao falhou: %v", err)
		}
	}

	restantes, err := diario.ContarCalibracoesPendentes(t.Context())
	if err != nil {
		t.Fatalf("contagem falhou: %v", err)
	}
	if restantes != 0 {
		t.Fatalf("sobraram %d linhas de calibracao", restantes)
	}
}

func TestCalibracaoRecusaAmostraNaoPositiva(t *testing.T) {
	t.Parallel()

	diario := diarioDeTeste(t)

	if _, err := diario.MedirCustoDeTransacao(t.Context(), 0); err == nil {
		t.Fatal("calibracao com zero amostras deveria falhar")
	}
}
