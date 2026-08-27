package no_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/no"
)

// TestClasseVemDaAnotacaoDoContrato verifica que a origem e o gateway derivam a
// classe de dado da MESMA declaracao.
//
// A anotacao classe_de_dado vive uma vez so, no .proto. Se a origem a resolvesse
// por um switch escrito a mao, os dois lados poderiam discordar sobre o que pode
// ser descartado — e a divergencia so apareceria no dia da falha, quando o buffer
// enchesse e a origem jogasse fora algo que o gateway considerava garantido.
func TestClasseVemDaAnotacaoDoContrato(t *testing.T) {
	casos := map[string]struct {
		envelope *contratov1.Envelope
		esperada aquisicao.ClasseDeDado
	}{
		"amostra escalar e amostra": {
			envelope: &contratov1.Envelope{Conteudo: &contratov1.Envelope_AmostraEscalar{
				AmostraEscalar: &contratov1.AmostraEscalar{Valor: proto.Float32(1)}}},
			esperada: aquisicao.ClasseAmostra,
		},
		"amostra agregada e amostra": {
			envelope: &contratov1.Envelope{Conteudo: &contratov1.Envelope_AmostraAgregada{
				AmostraAgregada: &contratov1.AmostraAgregada{}}},
			esperada: aquisicao.ClasseAmostra,
		},
		"saude da origem e amostra": {
			envelope: &contratov1.Envelope{Conteudo: &contratov1.Envelope_SaudeDaOrigem{
				SaudeDaOrigem: &contratov1.SaudeDaOrigem{}}},
			esperada: aquisicao.ClasseAmostra,
		},
		"mudanca de estado e evento discreto": {
			envelope: &contratov1.Envelope{Conteudo: &contratov1.Envelope_MudancaDeEstadoDeMaquina{
				MudancaDeEstadoDeMaquina: &contratov1.MudancaDeEstadoDeMaquina{}}},
			esperada: aquisicao.ClasseEventoDiscreto,
		},
		"leitura de contador e evento discreto": {
			envelope: &contratov1.Envelope{Conteudo: &contratov1.Envelope_LeituraDeContador{
				LeituraDeContador: &contratov1.LeituraDeContador{}}},
			esperada: aquisicao.ClasseEventoDiscreto,
		},
		"lacuna de buffer e evento discreto": {
			envelope: &contratov1.Envelope{Conteudo: &contratov1.Envelope_LacunaDeBuffer{
				LacunaDeBuffer: &contratov1.LacunaDeBuffer{}}},
			esperada: aquisicao.ClasseEventoDiscreto,
		},
	}

	for nome, caso := range casos {
		t.Run(nome, func(t *testing.T) {
			classes := no.ClassesDe([]*contratov1.Envelope{caso.envelope})
			if len(classes) != 1 {
				t.Fatalf("classes = %d, esperado 1", len(classes))
			}
			if classes[0] != caso.esperada {
				t.Errorf("classe = %v, esperado %v", classes[0], caso.esperada)
			}
		})
	}
}

// TestEnvelopeSemConteudoCaiNaGarantiaMaisForte documenta o comportamento de contorno.
//
// Errar para o lado de preservar dado e sempre o erro mais barato: guardar algo
// descartavel custa espaco, descartar algo garantido custa o dado.
func TestEnvelopeSemConteudoCaiNaGarantiaMaisForte(t *testing.T) {
	classes := no.ClassesDe([]*contratov1.Envelope{{NumeroDeSequencia: proto.Uint64(1)}})

	if classes[0] != aquisicao.ClasseEventoDiscreto {
		t.Errorf("classe = %v, esperado ClasseEventoDiscreto", classes[0])
	}
}

func TestTemEventoDiscretoDetectaPrioridadeDeDespacho(t *testing.T) {
	buffer := no.NovoBuffer(10)

	buffer.Acrescentar(envelopeComSequencia(1), aquisicao.ClasseAmostra)
	if buffer.TemEventoDiscreto() {
		t.Error("buffer so com amostras nao deveria exigir despacho imediato")
	}

	buffer.Acrescentar(envelopeComSequencia(2), aquisicao.ClasseEventoDiscreto)
	if !buffer.TemEventoDiscreto() {
		t.Error("evento discreto pendente deveria exigir despacho imediato")
	}

	buffer.Drenar(10)
	if buffer.TemEventoDiscreto() {
		t.Error("buffer drenado nao deveria reportar evento pendente")
	}
}
