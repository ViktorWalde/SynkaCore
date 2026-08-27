package simulacao_test

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/no/simulacao"
)

// camaraReproduzivel constroi a camara com semente fixa.
//
// Reprodutibilidade era um debito declarado da V1.x: o gerador era acessado
// estaticamente e nao aceitava semente, entao nenhum teste sobre o ruido podia ser
// deterministico. Aqui ela vem de graca.
func camaraReproduzivel() *simulacao.CamaraDeVacuo {
	return simulacao.NovaCamaraDeVacuo(rand.New(rand.NewPCG(1, 2)))
}

func TestFasesCobremOCicloInteiro(t *testing.T) {
	casos := map[time.Duration]simulacao.Fase{
		0:                 simulacao.FaseOciosoInicial,
		29 * time.Second:  simulacao.FaseOciosoInicial,
		30 * time.Second:  simulacao.FaseAquecimento,
		59 * time.Second:  simulacao.FaseAquecimento,
		60 * time.Second:  simulacao.FaseManutencao,
		119 * time.Second: simulacao.FaseManutencao,
		120 * time.Second: simulacao.FaseResfriamento,
		149 * time.Second: simulacao.FaseResfriamento,
		150 * time.Second: simulacao.FaseOciosoFinal,
		179 * time.Second: simulacao.FaseOciosoFinal,

		// O ciclo recomeca sozinho: a camara opera indefinidamente.
		180 * time.Second:             simulacao.FaseOciosoInicial,
		190 * time.Second:             simulacao.FaseOciosoInicial,
		10*time.Hour + 30*time.Second: simulacao.FaseAquecimento,
	}

	for decorrido, esperada := range casos {
		if obtida := simulacao.FaseEm(decorrido); obtida != esperada {
			t.Errorf("fase em %v = %v, esperado %v", decorrido, obtida, esperada)
		}
	}
}

// TestTemperaturaSegueOPerfilDoProcesso verifica que a serie tem a forma certa.
//
// A tolerancia acomoda o ruido de instrumento: com desvio padrao de 0,8 grau, tres
// desvios cobrem 99,7% das amostras, e apertar mais produziria um teste que falha
// sozinho de vez em quando — que e pior que nao testar.
func TestTemperaturaSegueOPerfilDoProcesso(t *testing.T) {
	camara := camaraReproduzivel()
	const tolerancia = 3.0

	casos := map[string]struct {
		decorrido time.Duration
		esperada  float32
	}{
		"ambiente no ocioso inicial":    {15 * time.Second, 25},
		"meio da rampa de aquecimento":  {45 * time.Second, 45},
		"patamar de processo":           {90 * time.Second, 65},
		"meio da rampa de resfriamento": {135 * time.Second, 45},
		"ambiente no ocioso final":      {165 * time.Second, 25},
	}

	for nome, caso := range casos {
		t.Run(nome, func(t *testing.T) {
			obtida := camara.TemperaturaEm(caso.decorrido)
			if diferenca := obtida - caso.esperada; diferenca > tolerancia || diferenca < -tolerancia {
				t.Errorf("temperatura em %v = %.2f, esperado %.2f +- %.1f",
					caso.decorrido, obtida, caso.esperada, tolerancia)
			}
		})
	}
}

// TestPressaoNuncaFicaNegativa protege contra dado fisicamente impossivel.
//
// O ruido pode empurrar o valor abaixo de zero perto do vacuo. Uma pressao
// absoluta negativa numa serie faz alguem duvidar do sistema inteiro — e com
// razao.
func TestPressaoNuncaFicaNegativa(t *testing.T) {
	camara := camaraReproduzivel()

	for decorrido := time.Duration(0); decorrido < 20*simulacao.DuracaoDoCiclo; decorrido += 100 * time.Millisecond {
		if pressao := camara.PressaoEm(decorrido); pressao < 0 {
			t.Fatalf("pressao em %v = %.3f kPa, valor fisicamente impossivel", decorrido, pressao)
		}
	}
}

// TestPressaoCaiComOAquecimento verifica o acoplamento entre as duas grandezas.
//
// A camara nao aquece com pressao ambiente dentro: o vacuo e puxado junto. Duas
// grandezas simuladas de forma independente produziriam uma correlacao que nao
// existe em equipamento real.
func TestPressaoCaiComOAquecimento(t *testing.T) {
	camara := camaraReproduzivel()

	noOcioso := camara.PressaoEm(15 * time.Second)
	noPatamar := camara.PressaoEm(90 * time.Second)

	if noPatamar >= noOcioso {
		t.Errorf("pressao no patamar (%.2f kPa) deveria ser menor que no ocioso (%.2f kPa)",
			noPatamar, noOcioso)
	}
}

// TestContagemDePecasEAcumuladaEMonotonica protege a propriedade que torna a
// contagem confiavel.
//
// Uma leitura acumulada perdida no caminho e reposta pela proxima: a perda se cura
// sozinha. Um incremento perdido some para sempre. Como contagem de producao e
// numero que o gestor usa para decidir, essa diferenca separa um relatorio
// confiavel de um plausivel.
func TestContagemDePecasEAcumuladaEMonotonica(t *testing.T) {
	var anterior uint64

	for decorrido := time.Duration(0); decorrido < 10*simulacao.DuracaoDoCiclo; decorrido += time.Second {
		atual := simulacao.CiclosCompletosEm(decorrido)
		if atual < anterior {
			t.Fatalf("contagem retrocedeu em %v: %d apos %d", decorrido, atual, anterior)
		}
		anterior = atual
	}

	if final := simulacao.CiclosCompletosEm(10 * simulacao.DuracaoDoCiclo); final != 10 {
		t.Errorf("apos 10 ciclos a contagem = %d, esperado 10", final)
	}
}

// TestSementeFixaProduzSerieReproduzivel e o teste que so existe porque o gerador
// passou a ser injetavel.
func TestSementeFixaProduzSerieReproduzivel(t *testing.T) {
	primeira := simulacao.NovaCamaraDeVacuo(rand.New(rand.NewPCG(42, 42)))
	segunda := simulacao.NovaCamaraDeVacuo(rand.New(rand.NewPCG(42, 42)))

	for decorrido := time.Duration(0); decorrido < simulacao.DuracaoDoCiclo; decorrido += 5 * time.Second {
		if primeira.TemperaturaEm(decorrido) != segunda.TemperaturaEm(decorrido) {
			t.Fatalf("a mesma semente produziu temperaturas diferentes em %v", decorrido)
		}
	}
}
