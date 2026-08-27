package no_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/no"
)

func envelopeComSequencia(sequencia uint64) *contratov1.Envelope {
	return &contratov1.Envelope{
		NumeroDeSequencia: proto.Uint64(sequencia),
		TempoLigadoMs:     proto.Uint64(sequencia * 1000),
		Conteudo: &contratov1.Envelope_AmostraEscalar{
			AmostraEscalar: &contratov1.AmostraEscalar{Valor: proto.Float32(float32(sequencia))},
		},
	}
}

func sequenciasDe(envelopes []*contratov1.Envelope) []uint64 {
	sequencias := make([]uint64, 0, len(envelopes))
	for _, envelope := range envelopes {
		sequencias = append(sequencias, envelope.GetNumeroDeSequencia())
	}
	return sequencias
}

// TestSaturacaoSacrificaAmostraAntesDeEvento e o teste que prova que as classes
// nao sao decorativas.
//
// Descartar pela idade pura sacrificaria uma parada de maquina para preservar uma
// leitura de temperatura — invertendo exatamente a garantia que as classes existem
// para declarar. A amostra tolera perda porque a proxima repoe quase a mesma
// informacao; o evento nao tem vizinho que o substitua.
func TestSaturacaoSacrificaAmostraAntesDeEvento(t *testing.T) {
	buffer := no.NovoBuffer(3)

	// O evento entra PRIMEIRO, entao e o mais antigo. Uma politica por idade o
	// descartaria; a politica por classe precisa preserva-lo.
	buffer.Acrescentar(envelopeComSequencia(1), aquisicao.ClasseEventoDiscreto)
	buffer.Acrescentar(envelopeComSequencia(2), aquisicao.ClasseAmostra)
	buffer.Acrescentar(envelopeComSequencia(3), aquisicao.ClasseAmostra)

	// Estoura a capacidade.
	buffer.Acrescentar(envelopeComSequencia(4), aquisicao.ClasseAmostra)

	drenados := sequenciasDe(buffer.Drenar(10))
	if len(drenados) != 3 {
		t.Fatalf("drenados = %d itens, esperado 3", len(drenados))
	}
	if drenados[0] != 1 {
		t.Errorf("o evento discreto (sequencia 1) foi descartado; buffer contem %v", drenados)
	}

	lacuna := buffer.TomarLacuna()
	if lacuna.Registros != 1 {
		t.Errorf("registros descartados = %d, esperado 1", lacuna.Registros)
	}
	if !lacuna.Vazia() && lacuna.PrimeiraSequencia != 0 {
		t.Error("descarte de amostra nao deveria produzir intervalo de lacuna de evento")
	}
}

// TestDescarteDeEventoRegistraOIntervaloPerdido cobre o caso em que a perda de
// evento e inevitavel.
//
// Quando so restam eventos, um precisa sair. O que NAO pode acontecer e ele sair
// em silencio: a lacuna precisa carregar quantos se perderam e em que intervalo,
// para que o buraco vire fato visivel no dado.
func TestDescarteDeEventoRegistraOIntervaloPerdido(t *testing.T) {
	buffer := no.NovoBuffer(2)

	for sequencia := uint64(1); sequencia <= 5; sequencia++ {
		buffer.Acrescentar(envelopeComSequencia(sequencia), aquisicao.ClasseEventoDiscreto)
	}

	lacuna := buffer.TomarLacuna()
	if lacuna.Registros != 3 {
		t.Errorf("registros perdidos = %d, esperado 3", lacuna.Registros)
	}
	if lacuna.PrimeiraSequencia != 1 {
		t.Errorf("primeira sequencia perdida = %d, esperado 1", lacuna.PrimeiraSequencia)
	}
	if lacuna.UltimaSequencia != 3 {
		t.Errorf("ultima sequencia perdida = %d, esperado 3", lacuna.UltimaSequencia)
	}

	// O buffer guardou as duas mais recentes, que e o que a politica manda.
	if drenados := sequenciasDe(buffer.Drenar(10)); len(drenados) != 2 || drenados[0] != 4 {
		t.Errorf("buffer contem %v, esperado as sequencias 4 e 5", drenados)
	}
}

// TestLacunaEReportadaUmaVezSo protege o indicador contra inflacao.
//
// Se a leitura nao zerasse a contabilidade, cada despacho reportaria de novo tudo o
// que ja foi perdido, e o numero cresceria sozinho. Um indicador que cresce sem
// que nada esteja acontecendo e pior que indicador nenhum.
func TestLacunaEReportadaUmaVezSo(t *testing.T) {
	buffer := no.NovoBuffer(1)

	buffer.Acrescentar(envelopeComSequencia(1), aquisicao.ClasseAmostra)
	buffer.Acrescentar(envelopeComSequencia(2), aquisicao.ClasseAmostra)

	if primeira := buffer.TomarLacuna(); primeira.Registros != 1 {
		t.Errorf("primeira leitura da lacuna = %d registros, esperado 1", primeira.Registros)
	}
	if segunda := buffer.TomarLacuna(); !segunda.Vazia() {
		t.Errorf("segunda leitura devolveu %d registros, esperado lacuna vazia", segunda.Registros)
	}
}

// TestDevolucaoPreservaAOrdemDeSequencia protege a confirmacao por faixa contigua.
//
// A confirmacao diz "duravel ate a sequencia N", o que so e expressavel se os
// numeros sairem em ordem. Devolver ao FIM do buffer faria o lote seguinte sair
// fora de ordem e a garantia deixaria de ser formulavel.
func TestDevolucaoPreservaAOrdemDeSequencia(t *testing.T) {
	buffer := no.NovoBuffer(10)

	for sequencia := uint64(1); sequencia <= 4; sequencia++ {
		buffer.Acrescentar(envelopeComSequencia(sequencia), aquisicao.ClasseAmostra)
	}

	// Despacha os dois primeiros e falha; enquanto isso, o ciclo continua amostrando.
	naoDespachados := buffer.Drenar(2)
	buffer.Acrescentar(envelopeComSequencia(5), aquisicao.ClasseAmostra)
	buffer.Devolver(naoDespachados, []aquisicao.ClasseDeDado{aquisicao.ClasseAmostra, aquisicao.ClasseAmostra})

	drenados := sequenciasDe(buffer.Drenar(10))
	esperado := []uint64{1, 2, 3, 4, 5}

	if len(drenados) != len(esperado) {
		t.Fatalf("drenados = %v, esperado %v", drenados, esperado)
	}
	for indice, sequencia := range esperado {
		if drenados[indice] != sequencia {
			t.Fatalf("ordem apos devolucao = %v, esperado %v", drenados, esperado)
		}
	}
}

// TestDevolucaoRespeitaACapacidade cobre o cenario de indisponibilidade prolongada.
//
// O buffer continuou recebendo enquanto o despacho estava em curso; devolver sem
// reaplicar a politica o faria crescer sem limite exatamente durante a queda, que e
// quando ele mais e exercitado.
func TestDevolucaoRespeitaACapacidade(t *testing.T) {
	buffer := no.NovoBuffer(4)

	for sequencia := uint64(1); sequencia <= 4; sequencia++ {
		buffer.Acrescentar(envelopeComSequencia(sequencia), aquisicao.ClasseAmostra)
	}
	naoDespachados := buffer.Drenar(4)

	// O gateway esta fora; o ciclo continua amostrando e reenche o buffer.
	for sequencia := uint64(5); sequencia <= 8; sequencia++ {
		buffer.Acrescentar(envelopeComSequencia(sequencia), aquisicao.ClasseAmostra)
	}

	buffer.Devolver(naoDespachados, nil)

	if ocupacao := buffer.Ocupacao(); ocupacao > 4 {
		t.Errorf("ocupacao apos devolucao = %d, acima da capacidade 4", ocupacao)
	}
	if lacuna := buffer.TomarLacuna(); lacuna.Registros != 4 {
		t.Errorf("descartes contabilizados = %d, esperado 4", lacuna.Registros)
	}
}

func TestOcupacaoEBytesRefletemOConteudo(t *testing.T) {
	buffer := no.NovoBuffer(10)

	if buffer.Ocupacao() != 0 || buffer.BytesEstimados() != 0 {
		t.Error("buffer recem-criado deveria estar vazio")
	}

	buffer.Acrescentar(envelopeComSequencia(1), aquisicao.ClasseAmostra)
	buffer.Acrescentar(envelopeComSequencia(2), aquisicao.ClasseAmostra)

	if ocupacao := buffer.Ocupacao(); ocupacao != 2 {
		t.Errorf("ocupacao = %d, esperado 2", ocupacao)
	}
	if bytes := buffer.BytesEstimados(); bytes <= 0 {
		t.Errorf("bytes estimados = %d, esperado valor positivo", bytes)
	}
}
