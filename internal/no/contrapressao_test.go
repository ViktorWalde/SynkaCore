package no_test

import (
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/no"
	"github.com/ViktorWalde/SynkaCore/internal/no/simulacao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/relogio"
)

// gatewayQueRecusa devolve 429 com o Retry-After indicado.
//
// Um servidor HTTP de verdade, e nao um duble do transportador, porque o que este
// teste verifica e a leitura do FIO: cabecalho ausente, ilegivel ou no formato de
// data sao coisas que so existem em HTTP, e um duble as apagaria justamente onde
// elas moram.
func gatewayQueRecusa(t *testing.T, retryAfter string) string {
	t.Helper()

	servidor := httptest.NewServer(http.HandlerFunc(
		func(escritor http.ResponseWriter, _ *http.Request) {
			if retryAfter != "" {
				escritor.Header().Set("Retry-After", retryAfter)
			}
			escritor.WriteHeader(http.StatusTooManyRequests)
		}))
	t.Cleanup(servidor.Close)
	return servidor.URL
}

func remessaMinima() *contratov1.Remessa {
	return &contratov1.Remessa{
		VersaoDoEsquema:  proto.Uint32(1),
		IdDaInstalacao:   proto.String("planta-piloto"),
		IdDoDispositivo:  proto.String("camara-de-vacuo-01"),
		IdDaSessaoDeBoot: proto.String("boot-1"),
		Envelopes: []*contratov1.Envelope{{
			NumeroDeSequencia: proto.Uint64(1),
			TempoLigadoMs:     proto.Uint64(1000),
			Conteudo: &contratov1.Envelope_AmostraEscalar{
				AmostraEscalar: &contratov1.AmostraEscalar{Valor: proto.Float32(24.5)},
			},
		}},
	}
}

func despacharContra(t *testing.T, destino string) error {
	t.Helper()

	transportador := no.NovoTransportadorHTTP(destino, 5*time.Second, nil)
	_, err := transportador.Despachar(t.Context(), remessaMinima())
	if err == nil {
		t.Fatal("o gateway recusou, mas o transportador nao devolveu erro")
	}
	return err
}

// TestOrigemLeAEsperaQueOGatewayMediu e o outro lado da V2.4.
//
// O gateway passou a dizer quanto esperar, e isso so vale se a origem escutar.
// Recuo exponencial existe porque quem recua nao tem informacao nenhuma; quando o
// gateway responde com um numero que ele mediu, insistir no palpite seria trocar
// informacao por ritual.
func TestOrigemLeAEsperaQueOGatewayMediu(t *testing.T) {
	t.Parallel()

	err := despacharContra(t, gatewayQueRecusa(t, "7"))

	if !falha.TemCategoria(err, falha.CategoriaRecursoEsgotado) {
		t.Fatalf("categoria = %v, esperado recurso esgotado", falha.CategoriaDe(err))
	}

	espera, gatewayPediu := no.EsperaSolicitada(err)
	if !gatewayPediu {
		t.Fatal("o gateway mandou Retry-After e a origem nao leu")
	}
	if espera != 7*time.Second {
		t.Fatalf("espera = %v, esperado 7s", espera)
	}
}

// TestSemRetryAfterAOrigemVoltaAoRecuoExponencial trava a degradacao.
//
// Um gateway mais antigo, ou um intermediario que corta cabecalhos, produz 429 sem
// Retry-After. Nesse caso a origem precisa voltar a adivinhar — adivinhar e pior
// que saber, e muito melhor que nao recuar: sem recuo nenhum ela devolveria a
// mesma carga imediatamente a um gateway que acabou de dizer que esta cheio.
func TestSemRetryAfterAOrigemVoltaAoRecuoExponencial(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"ausente":  "",
		"ilegivel": "logo mais",
		"zero":     "0",
		"negativo": "-5",

		// A forma de DATA do HTTP e deliberadamente ignorada. Uma origem sem relogio
		// de bateria nasce em 1970 e nao tem como interpretar um instante absoluto —
		// e a mesma limitacao que obrigou o gateway a servir tempo por UDP na V2.1.
		"data absoluta": "Wed, 28 Aug 2026 09:00:00 GMT",
	}

	for nome, cabecalho := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			err := despacharContra(t, gatewayQueRecusa(t, cabecalho))

			// A CATEGORIA CONTINUA CERTA. O que se perde e o numero, nunca o
			// diagnostico: a origem ainda sabe que foi saturacao, e portanto ainda
			// preserva o lote em vez de descarta-lo.
			if !falha.TemCategoria(err, falha.CategoriaRecursoEsgotado) {
				t.Fatalf("categoria = %v, esperado recurso esgotado", falha.CategoriaDe(err))
			}
			if _, gatewayPediu := no.EsperaSolicitada(err); gatewayPediu {
				t.Fatalf("cabecalho %q nao deveria produzir espera solicitada", cabecalho)
			}
		})
	}
}

// TestContrapressaoNaoEDescartavelPelaOrigem trava a decisao que custa dado.
//
// 429 e um 4xx, e a regra geral da origem para 4xx e DESCARTAR — o gateway nunca
// vai aceitar aquilo. Aqui a regra se inverte: o conteudo esta certo, o gateway
// apenas nao cabe agora, e descartar por causa disso perderia dado bom por um
// problema que se resolve sozinho em segundos.
//
// A trava esta na categoria, e nao num comentario: RecursoEsgotado e tratada pelo
// no junto com as transitorias, e o switch sem default obriga qualquer categoria
// nova a declarar de que lado ela fica.
func TestContrapressaoNaoEDescartavelPelaOrigem(t *testing.T) {
	t.Parallel()

	err := despacharContra(t, gatewayQueRecusa(t, "2"))

	if falha.TemCategoria(err, falha.CategoriaEntradaInvalida) {
		t.Fatal("saturacao classificada como entrada invalida faria a origem DESCARTAR o lote")
	}
	if !falha.TemCategoria(err, falha.CategoriaRecursoEsgotado) {
		t.Fatalf("categoria = %v, esperado recurso esgotado", falha.CategoriaDe(err))
	}
}

// TestJitterEspalhaAEsperaPedidaPeloGateway trava o que impede a falha parcial de
// virar total.
//
// O gateway manda o MESMO numero para a frota inteira. Sem espalhar, todas as
// origens voltariam no mesmo instante, e o gateway que estava apenas saturado
// receberia um pico sincronizado — que e como um sistema que estava lento cai de
// vez.
func TestJitterEspalhaAEsperaPedidaPeloGateway(t *testing.T) {
	t.Parallel()

	const espera = 10 * time.Second
	const fracao = 0.2

	// Os extremos do sorteio, que sao o que define a largura da janela.
	menor := no.ComJitter(espera, fracao, func() float64 { return 0 })
	maior := no.ComJitter(espera, fracao, func() float64 { return 0.999999 })

	if menor != 8*time.Second {
		t.Fatalf("extremo inferior = %v, esperado 8s", menor)
	}
	if maior < 11900*time.Millisecond || maior > 12*time.Second {
		t.Fatalf("extremo superior = %v, esperado proximo de 12s", maior)
	}

	// E o centro continua sendo a espera pedida: o jitter espalha, nao desloca.
	if centro := no.ComJitter(espera, fracao, func() float64 { return 0.5 }); centro != espera {
		t.Fatalf("centro = %v, esperado %v", centro, espera)
	}
}

// gatewaySaturavel e um gateway HTTP de verdade que comeca recusando por
// saturacao e depois passa a aceitar.
//
// HTTP de verdade, e nao um duble de Transportador, porque este teste exercita a
// costura inteira: o 429 sai do gateway como cabecalho, atravessa a rede, e a
// decisao do no depende de ele ter sido lido corretamente do fio. Um duble
// devolveria o erro ja pronto e pularia justamente a parte que pode estar errada.
type gatewaySaturavel struct {
	mutex     sync.Mutex
	saturado  bool
	recebidos []uint64
}

func (g *gatewaySaturavel) ServeHTTP(escritor http.ResponseWriter, requisicao *http.Request) {
	g.mutex.Lock()
	saturado := g.saturado
	g.mutex.Unlock()

	if saturado {
		// Sessenta segundos: muito acima do teto de recuo desta origem. O gateway
		// esta autorizado a pedir, e a origem esta autorizada a nao obedecer alem do
		// proprio limite — e o teste so passa se ela voltar antes disso.
		escritor.Header().Set("Retry-After", "60")
		escritor.WriteHeader(http.StatusTooManyRequests)
		return
	}

	bruto, err := io.ReadAll(io.LimitReader(requisicao.Body, 4<<20))
	if err != nil {
		escritor.WriteHeader(http.StatusInternalServerError)
		return
	}

	var remessa contratov1.Remessa
	if err := proto.Unmarshal(bruto, &remessa); err != nil {
		escritor.WriteHeader(http.StatusBadRequest)
		return
	}

	var maior uint64
	g.mutex.Lock()
	for _, envelope := range remessa.GetEnvelopes() {
		sequencia := envelope.GetNumeroDeSequencia()
		g.recebidos = append(g.recebidos, sequencia)
		if sequencia > maior {
			maior = sequencia
		}
	}
	g.mutex.Unlock()

	corpo, err := proto.Marshal(&contratov1.ConfirmacaoDeRemessa{
		DuravelAteASequencia: proto.Uint64(maior),
	})
	if err != nil {
		escritor.WriteHeader(http.StatusInternalServerError)
		return
	}
	escritor.Header().Set("Content-Type", "application/x-protobuf")
	escritor.WriteHeader(http.StatusOK)
	_, _ = escritor.Write(corpo)
}

func (g *gatewaySaturavel) definirSaturacao(saturado bool) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.saturado = saturado
}

func (g *gatewaySaturavel) sequenciasRecebidas() []uint64 {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	return append([]uint64(nil), g.recebidos...)
}

// TestSaturacaoNaoPerdeDadoEONoNaoObedeceAlemDoProprioTeto exercita a V2.4 de
// ponta a ponta, com HTTP de verdade e o laco do no rodando.
//
// Duas propriedades, e as duas custam dado quando erradas:
//
//  1. RECUSA NAO E PERDA. O lote volta ao buffer e e retransmitido; nenhuma
//     sequencia falta e nenhuma se repete fora de ordem quando o gateway aceita de
//     novo. E a mesma garantia que vale para queda de gateway, agora para o caso em
//     que ele esta de pe e cheio.
//  2. A ORIGEM OBEDECE ATE O PROPRIO TETO. O gateway pede sessenta segundos, muito
//     acima do teto desta origem. Se a origem obedecesse cegamente, ela ficaria
//     calada por um minuto e o teste estouraria — um gateway defeituoso, ou apenas
//     mal calibrado, poderia calar a frota inteira. Obedecer nao pode significar
//     parar de verificar.
func TestSaturacaoNaoPerdeDadoEONoNaoObedeceAlemDoProprioTeto(t *testing.T) {
	gateway := &gatewaySaturavel{saturado: true}
	servidor := httptest.NewServer(gateway)
	defer servidor.Close()

	configuracao := configuracaoDeTeste()
	origem := no.NovoNo(configuracao, simulacao.NovaCamaraDeVacuo(rand.New(rand.NewPCG(1, 2))),
		no.NovoTransportadorHTTP(servidor.URL, 5*time.Second, nil),
		relogio.Sistema(), rand.New(rand.NewPCG(3, 4)), registroSilencioso())

	ctx, cancelar := context.WithCancel(t.Context())
	concluido := make(chan struct{})
	go func() {
		defer close(concluido)
		_ = origem.Executar(ctx)
	}()

	// Enquanto satura, nada e aceito — e nada pode se perder.
	time.Sleep(400 * time.Millisecond)
	if recebidas := gateway.sequenciasRecebidas(); len(recebidas) != 0 {
		t.Fatalf("o gateway saturado registrou %d sequencias", len(recebidas))
	}

	gateway.definirSaturacao(false)
	// Metade do que o gateway pediu, e muitas vezes o teto da origem. Se a origem
	// obedecesse ao pedido, nada chegaria nesta janela.
	time.Sleep(600 * time.Millisecond)

	cancelar()
	<-concluido

	recebidas := gateway.sequenciasRecebidas()
	if len(recebidas) == 0 {
		t.Fatal("nada chegou apos a saturacao passar: a origem obedeceu ao pedido " +
			"do gateway alem do proprio teto de recuo")
	}
	if recebidas[0] != 1 {
		t.Errorf("a primeira sequencia entregue foi %d, esperado 1: o inicio do buffer se perdeu",
			recebidas[0])
	}
	for indice := 1; indice < len(recebidas); indice++ {
		if recebidas[indice] <= recebidas[indice-1] {
			t.Fatalf("ordem quebrada na posicao %d: %d apos %d",
				indice, recebidas[indice], recebidas[indice-1])
		}
	}
}
