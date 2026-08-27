package aquisicao_test

import (
	"testing"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
)

func chaveDeTeste(t *testing.T, dispositivo, sessao string, sequencia uint64) aquisicao.ChaveDeIdempotencia {
	t.Helper()
	idDoDispositivo, err := identidadededispositivo.AnalisarIDDoDispositivo(dispositivo)
	if err != nil {
		t.Fatalf("dispositivo de teste invalido: %v", err)
	}
	idDaSessao, err := identidadededispositivo.AnalisarIDDaSessaoDeBoot(sessao)
	if err != nil {
		t.Fatalf("sessao de teste invalida: %v", err)
	}
	return aquisicao.NovaChaveDeIdempotencia(idDoDispositivo, idDaSessao, sequencia)
}

// TestChavesDistintasNaoColidemNaFormaTextual e a verificacao que sustenta a
// deduplicacao inteira.
//
// A chave e o indice unico do diario. Se duas mensagens diferentes produzissem a
// mesma forma textual, uma delas seria descartada como duplicata — e a contagem
// de producao ficaria permanentemente errada, sem nada acusar. E o modo de falha
// mais caro deste sistema justamente por ser silencioso.
func TestChavesDistintasNaoColidemNaFormaTextual(t *testing.T) {
	vistas := make(map[string]string)

	casos := map[string]aquisicao.ChaveDeIdempotencia{
		"mesma sessao, sequencias vizinhas / 1": chaveDeTeste(t, "prensa-01", "boot-a", 1),
		"mesma sessao, sequencias vizinhas / 2": chaveDeTeste(t, "prensa-01", "boot-a", 2),
		"outra sessao, mesma sequencia":         chaveDeTeste(t, "prensa-01", "boot-b", 1),
		"outro dispositivo, mesma sequencia":    chaveDeTeste(t, "prensa-02", "boot-a", 1),
		"sequencia no maximo do uint64":         chaveDeTeste(t, "prensa-01", "boot-a", ^uint64(0)),

		// Este par cairia no MESMO texto se os componentes fossem concatenados sem
		// separador: "prensa01" + "boota" e "prensa" + "01boota" produzem a mesma
		// cadeia. E o separador que os mantem distintos.
		"fronteira / dispositivo longo": chaveDeTeste(t, "prensa01", "boota", 1),
		"fronteira / sessao longa":      chaveDeTeste(t, "prensa", "01boota", 1),
	}

	for nome, chave := range casos {
		texto := chave.String()
		if anterior, jaVista := vistas[texto]; jaVista {
			t.Errorf("colisao na forma textual %q: %q e %q", texto, anterior, nome)
		}
		vistas[texto] = nome
	}
}

// TestFormaTextualDaChaveEEstavel congela o formato.
//
// Ele e persistido como indice do diario. Muda-lo silenciosamente faria toda
// mensagem ja gravada deixar de ser reconhecida como duplicata, e a primeira
// retransmissao apos a atualizacao entraria de novo.
func TestFormaTextualDaChaveEEstavel(t *testing.T) {
	chave := chaveDeTeste(t, "linha-2-prensa-01", "boot-7f3a", 918_273)

	const esperado = "linha-2-prensa-01:boot-7f3a:918273"
	if obtido := chave.String(); obtido != esperado {
		t.Errorf("forma textual = %q, esperado %q", obtido, esperado)
	}
}

func TestChaveDevolveSeusComponentes(t *testing.T) {
	chave := chaveDeTeste(t, "prensa-01", "boot-a", 7)

	if chave.IDDoDispositivo().String() != "prensa-01" {
		t.Errorf("dispositivo = %q", chave.IDDoDispositivo())
	}
	if chave.IDDaSessaoDeBoot().String() != "boot-a" {
		t.Errorf("sessao = %q", chave.IDDaSessaoDeBoot())
	}
	if chave.NumeroDeSequencia() != 7 {
		t.Errorf("sequencia = %d", chave.NumeroDeSequencia())
	}
}

// TestOSeparadorNaoCabeNoAlfabetoDosIdentificadores fecha a unica porta pela qual
// a forma textual poderia ficar ambigua.
//
// A separacao so e confiavel enquanto nenhum componente puder CONTER o separador.
// Se um dia o alfabeto dos identificadores for afrouxado para aceitar ":", duas
// chaves distintas passariam a produzir o mesmo texto — e a deduplicacao
// descartaria dado bom, em silencio. Este teste faz esse afrouxamento reprovar o
// build em vez de virar defeito em campo.
func TestOSeparadorNaoCabeNoAlfabetoDosIdentificadores(t *testing.T) {
	if _, err := identidadededispositivo.AnalisarIDDoDispositivo("prensa:01"); err == nil {
		t.Error("o alfabeto do dispositivo aceitou o separador da chave de idempotencia")
	}
	if _, err := identidadededispositivo.AnalisarIDDaSessaoDeBoot("boot:a"); err == nil {
		t.Error("o alfabeto da sessao de boot aceitou o separador da chave de idempotencia")
	}
}
