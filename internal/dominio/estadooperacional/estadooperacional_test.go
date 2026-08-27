package estadooperacional_test

import (
	"testing"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/estadooperacional"
)

var instanteDeReferencia = time.Date(2026, time.August, 26, 14, 30, 0, 0, time.UTC)

// TestNotificarOMesmoEstadoNaoProduzTransicao protege duas coisas ao mesmo tempo:
// o log nao repete, e "desde" continua marcando o INICIO da situacao.
//
// Sem isso, uma projecao que tenta a cada dois segundos durante uma queda de tres
// horas emitiria 5.400 linhas identicas — e o health check responderia
// "degradado ha 2 segundos" o tempo todo, escondendo exatamente a informacao que
// o operador precisa.
func TestNotificarOMesmoEstadoNaoProduzTransicao(t *testing.T) {
	var transicoes int
	rastreador := estadooperacional.NovoRastreador(instanteDeReferencia,
		func(estadooperacional.Estado, estadooperacional.Estado) { transicoes++ })

	rastreador.Notificar(estadooperacional.Degradado, instanteDeReferencia.Add(time.Minute))
	inicioDaDegradacao := instanteDeReferencia.Add(time.Minute)

	for tentativa := range 100 {
		rastreador.Notificar(estadooperacional.Degradado,
			inicioDaDegradacao.Add(time.Duration(tentativa)*2*time.Second))
	}

	if transicoes != 1 {
		t.Errorf("transicoes notificadas = %d, esperado 1", transicoes)
	}

	estado, desde := rastreador.Atual()
	if estado != estadooperacional.Degradado {
		t.Errorf("estado = %v, esperado Degradado", estado)
	}
	if !desde.Equal(inicioDaDegradacao) {
		t.Errorf("desde = %v, esperado %v (o inicio da degradacao, nao a ultima tentativa)",
			desde, inicioDaDegradacao)
	}
}

func TestTransicoesNotificamAnteriorEAtual(t *testing.T) {
	type transicao struct{ anterior, atual estadooperacional.Estado }
	var observadas []transicao

	rastreador := estadooperacional.NovoRastreador(instanteDeReferencia,
		func(anterior, atual estadooperacional.Estado) {
			observadas = append(observadas, transicao{anterior, atual})
		})

	rastreador.Notificar(estadooperacional.Reconectando, instanteDeReferencia.Add(time.Second))
	rastreador.Notificar(estadooperacional.Degradado, instanteDeReferencia.Add(10*time.Second))
	rastreador.Notificar(estadooperacional.Conectado, instanteDeReferencia.Add(time.Minute))

	esperadas := []transicao{
		{estadooperacional.Conectado, estadooperacional.Reconectando},
		{estadooperacional.Reconectando, estadooperacional.Degradado},
		{estadooperacional.Degradado, estadooperacional.Conectado},
	}

	if len(observadas) != len(esperadas) {
		t.Fatalf("transicoes = %d, esperado %d", len(observadas), len(esperadas))
	}
	for indice, esperada := range esperadas {
		if observadas[indice] != esperada {
			t.Errorf("transicao %d = %v -> %v, esperado %v -> %v", indice,
				observadas[indice].anterior, observadas[indice].atual,
				esperada.anterior, esperada.atual)
		}
	}
}

// TestNasceConectado documenta a escolha do estado inicial.
//
// Nascer degradado faria todo gateway saudavel alarmar na partida — e um alarme
// que sempre dispara e um alarme que ninguem le.
func TestNasceConectado(t *testing.T) {
	rastreador := estadooperacional.NovoRastreador(instanteDeReferencia, nil)

	estado, desde := rastreador.Atual()
	if estado != estadooperacional.Conectado {
		t.Errorf("estado inicial = %v, esperado Conectado", estado)
	}
	if !desde.Equal(instanteDeReferencia) {
		t.Errorf("desde = %v, esperado %v", desde, instanteDeReferencia)
	}
}

// TestLeituraConcorrenteNaoQuebraOPar exercita o motivo de haver mutex em vez de
// dois campos atomicos independentes.
//
// Rodado com -race, ele pega tanto a corrida de dados quanto a leitura de um par
// inconsistente (estado novo com instante antigo).
func TestLeituraConcorrenteNaoQuebraOPar(t *testing.T) {
	rastreador := estadooperacional.NovoRastreador(instanteDeReferencia, nil)

	pronto := make(chan struct{})
	concluido := make(chan struct{})

	go func() {
		defer close(concluido)
		<-pronto
		for ciclo := range 1000 {
			estado := estadooperacional.Conectado
			if ciclo%2 == 0 {
				estado = estadooperacional.Degradado
			}
			rastreador.Notificar(estado, instanteDeReferencia.Add(time.Duration(ciclo)*time.Second))
		}
	}()

	close(pronto)
	for range 1000 {
		if estado, desde := rastreador.Atual(); estado == 0 || desde.IsZero() {
			t.Fatal("leitura devolveu par inconsistente")
		}
	}
	<-concluido
}
