package instalacao_test

import (
	"testing"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/instalacao"
)

func declarado(t *testing.T, indice uint32, nomeDaGrandeza, unidade string) instalacao.CanalDeclarado {
	t.Helper()

	var declarada instalacao.Grandeza
	if nomeDaGrandeza != "" {
		declarada = grandeza(t, nomeDaGrandeza)
	}
	return instalacao.CanalDeclarado{
		Endereco: aquisicao.EnderecoDeCanal{IndiceDoCanal: indice},
		Grandeza: declarada,
		Unidade:  unidade,
	}
}

func especies(divergencias []instalacao.Divergencia) []string {
	nomes := make([]string, 0, len(divergencias))
	for _, divergencia := range divergencias {
		nomes = append(nomes, divergencia.Especie.String())
	}
	return nomes
}

// TestOrigemQueBateComAConfiguracaoNaoProduzDivergencia e o caso saudavel.
func TestOrigemQueBateComAConfiguracaoNaoProduzDivergencia(t *testing.T) {
	configurada := instalacaoDeTeste(t)

	divergencias := configurada.ConferirDescritor(dispositivo(t, "camara-01"),
		[]instalacao.CanalDeclarado{
			declarado(t, 0, "temperatura", "Cel"),
			declarado(t, 1, "pressao", "kPa"),
		}, depoisDaTroca)

	if len(divergencias) != 0 {
		t.Errorf("instalacao correta produziu divergencias: %v", especies(divergencias))
	}
}

// TestCanalTrocadoNoPainelEDenunciado e a razao de esta verificacao existir.
//
// Dois cabos invertidos produzem duas series PLAUSIVEIS e ambas erradas. Sem esta
// conferencia, o erro so apareceria quando alguem estranhasse um numero — se
// aparecesse.
func TestCanalTrocadoNoPainelEDenunciado(t *testing.T) {
	configurada := instalacaoDeTeste(t)

	// O eletricista inverteu os dois cabos: a origem declara pressao onde a
	// instalacao espera temperatura, e vice-versa.
	divergencias := configurada.ConferirDescritor(dispositivo(t, "camara-01"),
		[]instalacao.CanalDeclarado{
			declarado(t, 0, "pressao", "kPa"),
			declarado(t, 1, "temperatura", "Cel"),
		}, depoisDaTroca)

	if len(divergencias) != 2 {
		t.Fatalf("divergencias = %v, esperado duas de grandeza", especies(divergencias))
	}
	for _, divergencia := range divergencias {
		if divergencia.Especie != instalacao.DivergenciaGrandeza {
			t.Errorf("especie = %v, esperado DivergenciaGrandeza", divergencia.Especie)
		}
		// O par declarado/esperado e o que resolve o problema. Sozinha, a especie
		// manda o tecnico procurar.
		if divergencia.Declarado == "" || divergencia.Esperado == "" {
			t.Errorf("divergencia sem o par declarado/esperado: %+v", divergencia)
		}
		if divergencia.Especie.AcaoCorretiva() == "" {
			t.Error("divergencia sem acao corretiva legivel")
		}
	}
}

// TestPontoConfiguradoQueNuncaRecebeDadoEDenunciado cobre a divergencia mais grave.
//
// Ausencia de dado NAO gera evento. Sem perguntar ativamente por ela, um ponto de
// medicao que nunca recebe nada e invisivel — ate alguem notar um grafico vazio
// semanas depois, quando a informacao daquele periodo ja se perdeu para sempre.
func TestPontoConfiguradoQueNuncaRecebeDadoEDenunciado(t *testing.T) {
	configurada := instalacaoDeTeste(t)

	// A origem so declara o canal 0; o canal 1 esta configurado e nao aparece.
	divergencias := configurada.ConferirDescritor(dispositivo(t, "camara-01"),
		[]instalacao.CanalDeclarado{declarado(t, 0, "temperatura", "Cel")}, depoisDaTroca)

	if len(divergencias) != 1 {
		t.Fatalf("divergencias = %v, esperado uma", especies(divergencias))
	}
	if divergencias[0].Especie != instalacao.DivergenciaCanalAusente {
		t.Errorf("especie = %v, esperado DivergenciaCanalAusente", divergencias[0].Especie)
	}
	if divergencias[0].Ponto != "curtimento.camara-01.pressao" {
		t.Errorf("ponto = %q", divergencias[0].Ponto)
	}
}

func TestCanalNaoConfiguradoEDenunciadoSemSerErro(t *testing.T) {
	configurada := instalacaoDeTeste(t)

	divergencias := configurada.ConferirDescritor(dispositivo(t, "camara-01"),
		[]instalacao.CanalDeclarado{
			declarado(t, 0, "temperatura", "Cel"),
			declarado(t, 1, "pressao", "kPa"),
			declarado(t, 7, "aceleracao_de_vibracao", "m/s2"),
		}, depoisDaTroca)

	if len(divergencias) != 1 {
		t.Fatalf("divergencias = %v, esperado uma", especies(divergencias))
	}
	if divergencias[0].Especie != instalacao.DivergenciaCanalNaoConfigurado {
		t.Errorf("especie = %v, esperado DivergenciaCanalNaoConfigurado", divergencias[0].Especie)
	}
}

// TestUnidadeDivergenteEDenunciada cobre o erro que passa mais despercebido.
//
// Mesma grandeza, escala errada: a serie continua parecendo razoavel, e so um
// numero muito fora do normal denunciaria — se alguem estivesse olhando.
func TestUnidadeDivergenteEDenunciada(t *testing.T) {
	configurada := instalacaoDeTeste(t)

	divergencias := configurada.ConferirDescritor(dispositivo(t, "camara-01"),
		[]instalacao.CanalDeclarado{
			declarado(t, 0, "temperatura", "K"), // kelvin em vez de celsius
			declarado(t, 1, "pressao", "kPa"),
		}, depoisDaTroca)

	if len(divergencias) != 1 {
		t.Fatalf("divergencias = %v, esperado uma", especies(divergencias))
	}
	if divergencias[0].Especie != instalacao.DivergenciaUnidade {
		t.Errorf("especie = %v, esperado DivergenciaUnidade", divergencias[0].Especie)
	}
	if divergencias[0].Declarado != "K" || divergencias[0].Esperado != "Cel" {
		t.Errorf("declarado/esperado = %q/%q", divergencias[0].Declarado, divergencias[0].Esperado)
	}
}

// TestOrigemQueNaoAfirmaNadaNaoDiverge separa "nao sei" de "discordo".
//
// Uma origem que envia o descritor sem grandeza nao esta discordando da
// configuracao — ela apenas nao afirmou nada. Tratar as duas igual encheria o
// relatorio de comissionamento de ruido vindo de origens simples, que nao tem
// autodeclaracao completa.
func TestOrigemQueNaoAfirmaNadaNaoDiverge(t *testing.T) {
	configurada := instalacaoDeTeste(t)

	divergencias := configurada.ConferirDescritor(dispositivo(t, "camara-01"),
		[]instalacao.CanalDeclarado{
			declarado(t, 0, "", ""),
			declarado(t, 1, "", ""),
		}, depoisDaTroca)

	if len(divergencias) != 0 {
		t.Errorf("origem sem autodeclaracao produziu divergencias: %v", especies(divergencias))
	}
}

// TestOrdemDoRelatorioEEstavel protege a comparacao entre execucoes.
//
// O relatorio de comissionamento e conferido duas vezes por quem esta no painel:
// antes e depois de mexer na fiacao. Uma listagem que muda de ordem a cada consulta
// — o que um mapa produz naturalmente — tornaria essa comparacao inutil.
func TestOrdemDoRelatorioEEstavel(t *testing.T) {
	configurada := instalacaoDeTeste(t)
	declarados := []instalacao.CanalDeclarado{
		declarado(t, 9, "temperatura", "Cel"),
		declarado(t, 7, "pressao", "kPa"),
		declarado(t, 8, "rotacao", "1/min"),
	}

	primeira := especies(configurada.ConferirDescritor(dispositivo(t, "camara-01"), declarados, depoisDaTroca))
	for range 20 {
		atual := configurada.ConferirDescritor(dispositivo(t, "camara-01"), declarados, depoisDaTroca)
		for indice, especie := range especies(atual) {
			if especie != primeira[indice] {
				t.Fatal("a ordem do relatorio de comissionamento variou entre execucoes")
			}
		}
	}
}

// TestDerivaDoCatalogoDeMotivosEDetectada cobre a mesma rede de protecao aplicada
// ao vocabulario.
//
// Se a origem exibe rotulos de uma versao antiga, os codigos que ela envia podem
// significar OUTRA COISA — e o dado fica errado de forma indetectavel, porque o
// codigo continua sendo um inteiro perfeitamente valido.
func TestDerivaDoCatalogoDeMotivosEDetectada(t *testing.T) {
	configurada := instalacaoDeTeste(t)

	if !configurada.ConferirVersaoDoCatalogoDeMotivos(1) {
		t.Error("a versao igual a do gateway nao deveria acusar deriva")
	}
	if configurada.ConferirVersaoDoCatalogoDeMotivos(2) {
		t.Error("versao divergente deveria acusar deriva")
	}

	// Versao zero significa "nao declarada": origem sem interface de operador nao
	// tem catalogo para carregar, e cobrar isso dela seria ruido.
	if !configurada.ConferirVersaoDoCatalogoDeMotivos(0) {
		t.Error("versao nao declarada nao deveria acusar deriva")
	}
}
