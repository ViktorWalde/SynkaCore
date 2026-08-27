package autoridadedetempo_test

import (
	"testing"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/autoridadedetempo"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/relogio"
)

var instanteDeReferencia = time.Date(2026, time.August, 26, 14, 30, 0, 0, time.UTC)

func ancoraDeTeste(t *testing.T, tempoLigado time.Duration, r relogio.Relogio) autoridadedetempo.AncoraDeSessaoDeBoot {
	t.Helper()

	idDoDispositivo, err := identidadededispositivo.AnalisarIDDoDispositivo("prensa-01")
	if err != nil {
		t.Fatalf("dispositivo de teste invalido: %v", err)
	}
	idDaSessao, err := identidadededispositivo.AnalisarIDDaSessaoDeBoot("boot-7f3a")
	if err != nil {
		t.Fatalf("sessao de teste invalida: %v", err)
	}

	ancora, err := autoridadedetempo.NovaAncoraDeSessaoDeBoot(
		idDoDispositivo, idDaSessao, tempoLigado, r.Agora(), r.Decorrido())
	if err != nil {
		t.Fatalf("ancora de teste deveria ser construida: %v", err)
	}
	return ancora
}

// TestEstimaInstanteAPartirDoTempoLigado cobre o caminho normal: a origem so sabe
// ha quanto tempo esta ligada, e o gateway traduz isso para hora do dia.
func TestEstimaInstanteAPartirDoTempoLigado(t *testing.T) {
	r := relogio.NovoFalso(instanteDeReferencia)
	ancora := ancoraDeTeste(t, 30*time.Second, r)

	estimado, err := ancora.EstimarInstanteDaAmostra(90 * time.Second)
	if err != nil {
		t.Fatalf("estimativa deveria ser possivel: %v", err)
	}

	// A amostra veio 60 s depois do ponto de ancoragem, entao o instante estimado
	// e 60 s depois do instante de ancoragem.
	esperado := instanteDeReferencia.Add(60 * time.Second)
	if !estimado.Equal(esperado) {
		t.Errorf("instante estimado = %v, esperado %v", estimado, esperado)
	}
}

// TestRecusaTempoLigadoAnteriorAAncora protege contra a origem que reiniciou sem
// sortear nova sessao de boot.
//
// O tempo ligado e monotonico DENTRO de uma sessao. Se ele anda para tras, ou a
// origem reiniciou sem renovar a sessao, ou o quadro esta corrompido. Nos dois
// casos a serie daquela sessao deixou de ser confiavel, e estimar assim mesmo
// produziria um instante no passado que parece perfeitamente plausivel.
func TestRecusaTempoLigadoAnteriorAAncora(t *testing.T) {
	r := relogio.NovoFalso(instanteDeReferencia)
	ancora := ancoraDeTeste(t, 60*time.Second, r)

	if _, err := ancora.EstimarInstanteDaAmostra(59 * time.Second); err == nil {
		t.Fatal("tempo ligado anterior a ancora deveria ser recusado")
	} else if !falha.TemCategoria(err, falha.CategoriaEntradaInvalida) {
		t.Errorf("categoria = %v, esperado CategoriaEntradaInvalida", falha.CategoriaDe(err))
	}
}

// TestAncoraExigeSeusComponentes verifica que uma ancora incompleta nunca existe.
func TestAncoraExigeSeusComponentes(t *testing.T) {
	dispositivo, _ := identidadededispositivo.AnalisarIDDoDispositivo("prensa-01")
	sessao, _ := identidadededispositivo.AnalisarIDDaSessaoDeBoot("boot-7f3a")

	casos := map[string]func() error{
		"sem dispositivo": func() error {
			_, err := autoridadedetempo.NovaAncoraDeSessaoDeBoot(
				identidadededispositivo.IDDoDispositivo{}, sessao, 0, instanteDeReferencia, 0)
			return err
		},
		"sem sessao de boot": func() error {
			_, err := autoridadedetempo.NovaAncoraDeSessaoDeBoot(
				dispositivo, identidadededispositivo.IDDaSessaoDeBoot{}, 0, instanteDeReferencia, 0)
			return err
		},
		"sem instante de observacao": func() error {
			_, err := autoridadedetempo.NovaAncoraDeSessaoDeBoot(
				dispositivo, sessao, 0, time.Time{}, 0)
			return err
		},
		"tempo ligado negativo": func() error {
			_, err := autoridadedetempo.NovaAncoraDeSessaoDeBoot(
				dispositivo, sessao, -time.Second, instanteDeReferencia, 0)
			return err
		},
		"leitura monotonica negativa": func() error {
			_, err := autoridadedetempo.NovaAncoraDeSessaoDeBoot(
				dispositivo, sessao, 0, instanteDeReferencia, -time.Second)
			return err
		},
	}

	for nome, construir := range casos {
		t.Run(nome, func(t *testing.T) {
			if err := construir(); err == nil {
				t.Fatal("ancora incompleta deveria ser recusada")
			}
		})
	}
}

// TestDegrauDeRelogioEDetectado e o teste que resolve o achado que estava
// bloqueando o projeto.
//
// O problema: em Go, time.Time carrega uma leitura monotonica, mas .UTC() a
// DESCARTA. A partir dali, subtrair dois instantes usa o relogio de PAREDE — que
// anda para tras quando o NTP corrige. Uma ancora feita antes de um acerto de
// hora passaria a derivar instantes deslocados, em silencio, e o dado resultante
// seria PLAUSIVEL.
//
// Numa trilha que precisa provar QUANDO algo aconteceu, isso deixa de ser
// corretude e vira conformidade: nao se pode provar o instante se o relogio pode
// retroceder sem deixar vestigio.
//
// A solucao: a ancora guarda a parede E o monotonico. Um degrau move so a parede,
// entao a divergencia entre os dois VIRA MENSURAVEL. E este teste consegue
// provocar o degrau — que era o outro lado da dificuldade, porque um degrau real
// e impossivel de reproduzir em integracao continua.
func TestDegrauDeRelogioEDetectado(t *testing.T) {
	casos := map[string]struct {
		degrau         time.Duration
		esperaDeteccao bool
	}{
		"operacao normal, sem degrau":            {degrau: 0, esperaDeteccao: false},
		"deriva menor que a tolerancia":          {degrau: 200 * time.Millisecond, esperaDeteccao: false},
		"NTP adianta o relogio":                  {degrau: 45 * time.Second, esperaDeteccao: true},
		"NTP atrasa o relogio":                   {degrau: -45 * time.Second, esperaDeteccao: true},
		"alguem acerta a hora na mao, para tras": {degrau: -2 * time.Hour, esperaDeteccao: true},
	}

	for nome, caso := range casos {
		t.Run(nome, func(t *testing.T) {
			r := relogio.NovoFalso(instanteDeReferencia)
			ancora := ancoraDeTeste(t, 30*time.Second, r)

			// Cinco minutos de operacao normal: as duas leituras andam juntas.
			r.Avancar(5 * time.Minute)

			// E entao o relogio de parede da um degrau sozinho, que e exatamente o
			// que um acerto de hora faz.
			r.DarDegrau(caso.degrau)

			err := ancora.VerificarDegrauDeRelogio(
				r.Agora(), r.Decorrido(), autoridadedetempo.ToleranciaDeDegrauPadrao)

			if caso.esperaDeteccao && err == nil {
				desvio := ancora.DesvioDeRelogio(r.Agora(), r.Decorrido())
				t.Errorf("degrau de %v nao foi detectado (desvio medido: %v)", caso.degrau, desvio)
			}
			if !caso.esperaDeteccao && err != nil {
				t.Errorf("operacao normal acusou degrau indevidamente: %v", err)
			}
		})
	}
}

// TestDegrauDeRelogioECulpaDoGateway verifica a classificacao da falha.
//
// Quem errou foi o gateway, nao a origem. Se fosse CategoriaEntradaInvalida, o
// adaptador responderia 400 e mandaria a origem DESCARTAR dado bom por causa de um
// problema do relogio do gateway — perder dado real para encobrir defeito nosso.
func TestDegrauDeRelogioECulpaDoGateway(t *testing.T) {
	r := relogio.NovoFalso(instanteDeReferencia)
	ancora := ancoraDeTeste(t, 30*time.Second, r)

	r.Avancar(time.Minute)
	r.DarDegrau(-time.Hour)

	err := ancora.VerificarDegrauDeRelogio(
		r.Agora(), r.Decorrido(), autoridadedetempo.ToleranciaDeDegrauPadrao)
	if err == nil {
		t.Fatal("degrau de uma hora deveria ser detectado")
	}
	if !falha.TemCategoria(err, falha.CategoriaInterna) {
		t.Errorf("categoria = %v, esperado CategoriaInterna", falha.CategoriaDe(err))
	}
}

// TestRelogioDoSistemaSeparaParedeDeMonotonico verifica que a implementacao real
// — e nao so a falsa usada nos demais testes — mantem as duas leituras distintas.
//
// Sem isto, todo o resto seria verificado apenas contra um dublê, e o defeito
// original (o .UTC() que descarta o monotonico) poderia voltar sem nada acusar.
func TestRelogioDoSistemaSeparaParedeDeMonotonico(t *testing.T) {
	r := relogio.Sistema()

	paredeAntes := r.Agora()
	decorridoAntes := r.Decorrido()

	const pausa = 20 * time.Millisecond
	time.Sleep(pausa)

	paredeDepois := r.Agora()
	decorridoDepois := r.Decorrido()

	avancoDaParede := paredeDepois.Sub(paredeAntes)
	avancoMonotonico := decorridoDepois - decorridoAntes

	if avancoMonotonico < pausa {
		t.Errorf("o monotonico avancou %v, esperado ao menos %v", avancoMonotonico, pausa)
	}
	if avancoDaParede <= 0 {
		t.Errorf("a parede avancou %v, esperado avanco positivo", avancoDaParede)
	}

	// As duas leituras precisam concordar em operacao normal. Divergencia grande
	// aqui significaria que uma delas nao esta medindo o que diz medir.
	desvio := avancoDaParede - avancoMonotonico
	if desvio < 0 {
		desvio = -desvio
	}
	if desvio > 50*time.Millisecond {
		t.Errorf("parede e monotonico divergiram %v em operacao normal", desvio)
	}
}
