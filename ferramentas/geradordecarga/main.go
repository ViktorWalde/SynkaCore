// Command geradordecarga responde "quantos dispositivos o gateway aguenta?".
//
// A pergunta esta em aberto desde a V1.x, onde a resposta era "estimativa
// conservadora: 3 a 5 dispositivos". Estimativa nao e resposta defensavel quando um
// cliente pergunta, e uma estimativa conservadora que nunca foi medida costuma estar
// errada nos dois sentidos.
//
// COMO ELE MEDE, e por que assim. Origens virtuais falando HTTP de verdade com um
// gateway de verdade, cada uma com identidade e sessao de boot proprias. Medir
// chamando o servico em processo seria mais simples e mediria outra coisa: ficariam
// de fora serializacao, rede, contencao no diario e o custo por conexao — que sao
// justamente os candidatos a gargalo.
//
// O QUE ELE NAO MEDE: o comportamento sob falha. Ele mede o caminho feliz saturado.
// Queda de gateway e recuperacao sao exercitadas pelo teste de ponta a ponta.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
)

const (
	tipoDeConteudoProtobuf = "application/x-protobuf"

	// amostrasDeLatenciaPorOrigem limita o que cada origem guarda para o resumo.
	//
	// Guardar toda medicao numa rodada longa consumiria memoria do proprio gerador e
	// falsearia o resultado: uma ferramenta que compete por recurso com o que ela
	// mede nao mede nada confiavel.
	amostrasDeLatenciaPorOrigem = 10_000
)

func main() {
	destino := flag.String("gateway", "http://127.0.0.1:8443/ingestao", "caminho de ingestao")
	origens := flag.Int("origens", 10, "quantas origens virtuais simultaneas")
	porRemessa := flag.Int("lote", 100, "envelopes por remessa")
	intervalo := flag.Duration("intervalo", time.Second, "intervalo entre remessas de cada origem")
	duracao := flag.Duration("duracao", 30*time.Second, "quanto tempo manter a carga")
	classe := flag.String("classe", "amostra",
		"o que as origens emitem: amostra ou evento (decide qual orcamento de admissao vale)")
	flag.Parse()

	// A classe NAO e um enfeite do gerador: ela decide qual orcamento de espera o
	// gateway aplica a estas origens. Sem poder emitir evento discreto, uma rodada de
	// carga mediria apenas metade da politica de admissao — e a metade que ela
	// deixaria de fora e justamente a que existe para proteger o dado sem substituto.
	emitirEvento := *classe == "evento"
	if !emitirEvento && *classe != "amostra" {
		fmt.Fprintf(os.Stderr, "classe desconhecida: %q (use amostra ou evento)\n", *classe)
		os.Exit(2)
	}

	ctx, encerrar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer encerrar()

	ctx, expirar := context.WithTimeout(ctx, *duracao)
	defer expirar()

	fmt.Printf("carga: %d origens x %d envelopes de %s a cada %v, por %v\n",
		*origens, *porRemessa, *classe, *intervalo, *duracao)
	fmt.Printf("alvo nominal: %.0f envelopes/s\n\n",
		float64(*origens)*float64(*porRemessa)/intervalo.Seconds())

	resultado := aplicarCarga(ctx, *destino, *origens, *porRemessa, *intervalo, emitirEvento)
	resultado.relatar()
}

// medicao acumula o resultado de uma rodada.
type medicao struct {
	remessasAceitas   atomic.Int64
	remessasRecusadas atomic.Int64
	envelopesAceitos  atomic.Int64
	falhasDeRede      atomic.Int64

	// contrapressao conta as remessas que o gateway recusou com 429, e ela e
	// SEPARADA das demais recusas por uma razao que decide a leitura da rodada.
	//
	// Ate a V2.3 o gateway nao produzia 429, e saturacao aparecia aqui como "falhas
	// de rede": o gateway deixava de responder dentro do tempo limite. Somar as duas
	// agora apagaria a diferenca entre o sistema SE PROTEGENDO — que e o resultado
	// desejado — e o sistema quebrando, que e o resultado a evitar.
	contrapressao atomic.Int64

	// esperaPedidaMaxima e o maior Retry-After observado, em segundos.
	//
	// O maximo, e nao a media: ele responde "qual foi o pior momento da rodada",
	// que e a pergunta que dimensiona o buffer da origem. Uma media diluiria o
	// pico num mar de respostas rapidas.
	esperaPedidaMaxima atomic.Int64

	// interrompidas conta as remessas em voo quando a janela de medicao terminou.
	//
	// Separadas das falhas de rede de proposito: elas nao sao falha do gateway, e
	// contá-las junto faria toda rodada terminar reportando "N falhas" — treinando
	// quem le a ignorar o numero justamente quando ele significar algo.
	interrompidas atomic.Int64

	mutex     sync.Mutex
	latencias []time.Duration
	decorrido time.Duration
}

func (m *medicao) registrarLatencia(latencia time.Duration) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	if len(m.latencias) < amostrasDeLatenciaPorOrigem*64 {
		m.latencias = append(m.latencias, latencia)
	}
}

// aplicarCarga sobe as origens virtuais e mantem a carga ate o contexto expirar.
func aplicarCarga(ctx context.Context, destino string, origens, porRemessa int,
	intervalo time.Duration, emitirEvento bool) *medicao {

	resultado := &medicao{}
	inicio := time.Now()

	var trabalhando sync.WaitGroup
	trabalhando.Add(origens)

	for indice := range origens {
		go func(numero int) {
			defer trabalhando.Done()
			operarOrigem(ctx, destino, numero, porRemessa, intervalo, emitirEvento, resultado)
		}(indice)
	}

	trabalhando.Wait()
	resultado.decorrido = time.Since(inicio)
	return resultado
}

// operarOrigem simula um dispositivo entregando remessas em cadencia fixa.
func operarOrigem(ctx context.Context, destino string, numero, porRemessa int,
	intervalo time.Duration, emitirEvento bool, resultado *medicao) {

	// Cada origem tem identidade PROPRIA. Reusar o mesmo identificador faria as
	// remessas colidirem na chave de idempotencia e serem descartadas como
	// duplicatas — a medicao reportaria vazao alta com o diario praticamente vazio.
	dispositivo := fmt.Sprintf("carga-%03d", numero)
	sessao := "boot-" + sortear()

	cliente := &http.Client{Timeout: 30 * time.Second}
	temporizador := time.NewTicker(intervalo)
	defer temporizador.Stop()

	var sequencia uint64

	for {
		select {
		case <-ctx.Done():
			return
		case <-temporizador.C:
		}

		corpo, primeira := montarRemessa(dispositivo, sessao, sequencia, porRemessa, emitirEvento)
		sequencia = primeira

		inicio := time.Now()
		desfecho, err := entregar(ctx, cliente, destino, corpo)
		latencia := time.Since(inicio)

		switch {
		case err != nil && ctx.Err() != nil:
			// A janela de medicao acabou com a requisicao em voo. Nao e falha.
			resultado.interrompidas.Add(1)
		case err != nil:
			resultado.falhasDeRede.Add(1)
		case desfecho.aceita:
			resultado.remessasAceitas.Add(1)
			resultado.envelopesAceitos.Add(int64(porRemessa))
			resultado.registrarLatencia(latencia)
		case desfecho.contrapressao:
			// A CADENCIA NAO MUDA. O gerador NAO honra o Retry-After, e essa e uma
			// decisao de instrumento: ele existe para descobrir onde o gateway satura,
			// e recuar quando ele pede reduziria a carga exatamente no ponto que a
			// rodada quer medir. Uma origem de verdade recua; um instrumento de
			// medicao insiste, e reporta o que ouviu.
			resultado.contrapressao.Add(1)
			resultado.registrarEsperaPedida(desfecho.esperaPedida)
		default:
			resultado.remessasRecusadas.Add(1)
		}
	}
}

// registrarEsperaPedida guarda o maior Retry-After observado na rodada.
func (m *medicao) registrarEsperaPedida(segundos int64) {
	for {
		maior := m.esperaPedidaMaxima.Load()
		if segundos <= maior || m.esperaPedidaMaxima.CompareAndSwap(maior, segundos) {
			return
		}
	}
}

// montarRemessa serializa um lote e devolve o proximo numero de sequencia.
func montarRemessa(dispositivo, sessao string, sequencia uint64, quantidade int,
	emitirEvento bool) ([]byte, uint64) {

	envelopes := make([]*contratov1.Envelope, 0, quantidade)

	for range quantidade {
		sequencia++
		envelope := &contratov1.Envelope{
			NumeroDeSequencia: proto.Uint64(sequencia),
			TempoLigadoMs:     proto.Uint64(sequencia * 100),
		}
		aplicarConteudo(envelope, emitirEvento)
		envelopes = append(envelopes, envelope)
	}

	corpo, err := proto.Marshal(&contratov1.Remessa{
		VersaoDoEsquema:  proto.Uint32(1),
		IdDaInstalacao:   proto.String("carga"),
		IdDoDispositivo:  proto.String(dispositivo),
		IdDaSessaoDeBoot: proto.String(sessao),
		Envelopes:        envelopes,
	})
	if err != nil {
		// Alcancavel apenas com defeito no gerador. Falhar alto e melhor que
		// reportar vazao de remessas que nunca foram montadas.
		panic("geradordecarga: serializacao falhou: " + err.Error())
	}
	return corpo, sequencia
}

// desfechoDaEntrega distingue os TRES resultados possiveis de uma remessa.
//
// Tres, e nao dois. "Aceita ou nao" era suficiente enquanto o gateway so sabia
// aceitar ou quebrar; com contrapressao explicita, recusa deliberada e recusa por
// defeito passam a significar coisas opostas, e um booleano as juntaria de novo.
type desfechoDaEntrega struct {
	aceita        bool
	contrapressao bool

	// esperaPedida e o Retry-After em segundos, quando houve contrapressao.
	esperaPedida int64
}

// aplicarConteudo preenche o conteudo do envelope conforme a classe pedida.
//
// Escreve no envelope em vez de devolver o conteudo porque a interface do oneof
// gerada pelo protobuf NAO e exportada — e ela nao ser exportada e correto: o
// conjunto de conteudos possiveis pertence ao contrato, e ninguem de fora deveria
// conseguir declarar um.
//
// A classe nao e escolhida aqui: escolhe-se o TIPO DE CONTEUDO, e a classe vem
// junto pela anotacao do .proto, que o gateway le por reflexao. Do contrario a
// rodada mediria a politica que o gerador afirma, e nao a que o gateway aplica.
func aplicarConteudo(envelope *contratov1.Envelope, emitirEvento bool) {
	endereco := &contratov1.EnderecoDeCanal{IndiceDoCanal: proto.Uint32(0)}

	if emitirEvento {
		envelope.Conteudo = &contratov1.Envelope_MudancaDeEstadoDeMaquina{
			MudancaDeEstadoDeMaquina: &contratov1.MudancaDeEstadoDeMaquina{
				Endereco: endereco,
				Estado:   contratov1.EstadoDeMaquina_ESTADO_DE_MAQUINA_PARADA.Enum(),
			},
		}
		return
	}
	envelope.Conteudo = &contratov1.Envelope_AmostraEscalar{
		AmostraEscalar: &contratov1.AmostraEscalar{
			Endereco: endereco,
			Valor:    proto.Float32(24.5),
		},
	}
}

func entregar(ctx context.Context, cliente *http.Client, destino string,
	corpo []byte) (desfechoDaEntrega, error) {

	requisicao, err := http.NewRequestWithContext(ctx, http.MethodPost, destino, bytes.NewReader(corpo))
	if err != nil {
		return desfechoDaEntrega{}, err
	}
	requisicao.Header.Set("Content-Type", tipoDeConteudoProtobuf)

	resposta, err := cliente.Do(requisicao)
	if err != nil {
		return desfechoDaEntrega{}, err
	}
	defer func() { _ = resposta.Body.Close() }()

	// O corpo e drenado mesmo sem ser usado: sem isso a conexao nao volta ao pool e
	// cada remessa abriria uma nova, medindo custo de conexao em vez de ingestao.
	_, _ = io.Copy(io.Discard, resposta.Body)

	if resposta.StatusCode == http.StatusTooManyRequests {
		espera, _ := strconv.ParseInt(resposta.Header.Get("Retry-After"), 10, 64)
		return desfechoDaEntrega{contrapressao: true, esperaPedida: espera}, nil
	}
	return desfechoDaEntrega{aceita: resposta.StatusCode == http.StatusOK}, nil
}

func sortear() string {
	bruto := make([]byte, 6)
	if _, err := rand.Read(bruto); err != nil {
		panic("geradordecarga: entropia indisponivel")
	}
	return hex.EncodeToString(bruto)
}

// relatar imprime o resultado da rodada.
func (m *medicao) relatar() {
	aceitas := m.remessasAceitas.Load()
	envelopes := m.envelopesAceitos.Load()
	segundos := m.decorrido.Seconds()

	fmt.Printf("decorrido            : %.1f s\n", segundos)
	fmt.Printf("remessas aceitas     : %d  (%.1f/s)\n", aceitas, float64(aceitas)/segundos)
	fmt.Printf("envelopes aceitos    : %d  (%.0f/s)\n", envelopes, float64(envelopes)/segundos)

	if contrapressao := m.contrapressao.Load(); contrapressao > 0 {
		// NAO e falha, e a linha diz isso. O gateway recusou de proposito, pediu uma
		// espera e preservou o dado do outro lado — uma origem de verdade devolve o
		// lote ao buffer e retransmite. Relatar isso como erro treinaria quem le a
		// ignorar o numero que informa saturacao.
		fmt.Printf("contrapressao (429)  : %d  (recusa deliberada; o gateway pediu ate %d s)\n",
			contrapressao, m.esperaPedidaMaxima.Load())
	}
	if recusadas := m.remessasRecusadas.Load(); recusadas > 0 {
		fmt.Printf("remessas RECUSADAS   : %d\n", recusadas)
	}
	if falhas := m.falhasDeRede.Load(); falhas > 0 {
		// Falha de rede sob carga significa que o gateway nao respondeu dentro do
		// tempo limite. Ate a V2.3 esse era o unico sinal de saturacao que existia;
		// com contrapressao explicita, ele passou a indicar algo mais grave — o
		// gateway ficou preso ANTES de conseguir dizer que estava cheio.
		fmt.Printf("falhas de rede       : %d  (o gateway nao respondeu a tempo)\n", falhas)
	}
	if interrompidas := m.interrompidas.Load(); interrompidas > 0 {
		fmt.Printf("em voo no fim        : %d  (esperado: a janela terminou)\n", interrompidas)
	}

	m.mutex.Lock()
	latencias := append([]time.Duration(nil), m.latencias...)
	m.mutex.Unlock()

	if len(latencias) == 0 {
		fmt.Println("\nnenhuma remessa foi aceita: o gateway esta no ar?")
		return
	}
	sort.Slice(latencias, func(primeiro, segundo int) bool {
		return latencias[primeiro] < latencias[segundo]
	})

	// Percentis, e nao media. A media esconde exatamente o que interessa aqui: uma
	// cauda de remessas lentas que, na origem, significa buffer enchendo.
	fmt.Println("\nlatencia da remessa (recepcao ate confirmacao durável):")
	for _, percentil := range []struct {
		nome   string
		fracao float64
	}{{"p50", 0.50}, {"p95", 0.95}, {"p99", 0.99}, {"max", 1.0}} {
		indice := int(float64(len(latencias)-1) * percentil.fracao)
		fmt.Printf("  %-4s %v\n", percentil.nome, latencias[indice].Round(time.Microsecond))
	}
}
