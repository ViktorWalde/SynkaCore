package no

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/protobuf/proto"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const (
	operacaoDespachar = "no.Despachar"

	// tipoDeConteudoProtobuf e o formato do fio.
	tipoDeConteudoProtobuf = "application/x-protobuf"

	// tamanhoMaximoDaConfirmacao limita a resposta que a origem aceita ler.
	//
	// A origem tambem nao confia no gateway sem limite: uma resposta infinita de um
	// gateway defeituoso — ou de algo que se passa por ele — travaria a origem sem
	// nada acusar.
	tamanhoMaximoDaConfirmacao = 64 << 10
)

// Transportador entrega uma remessa ao gateway e devolve a confirmacao.
//
// Interface, e nao a implementacao HTTP direto, por uma razao concreta e nao por
// purismo: ela permite exercitar o laco da origem contra gateway indisponivel,
// contrapressao e confirmacao parcial sem subir rede nenhuma. Esses tres cenarios
// sao o que decide se este componente funciona, e sao justamente os mais caros de
// reproduzir de verdade.
type Transportador interface {
	Despachar(ctx context.Context, remessa *contratov1.Remessa) (*contratov1.ConfirmacaoDeRemessa, error)
}

// TransportadorHTTP entrega remessas ao gateway por HTTP.
type TransportadorHTTP struct {
	cliente *http.Client
	destino string
}

// NovoTransportadorHTTP constroi o transportador.
//
// O tempo limite e do CLIENTE inteiro, e nao apenas da conexao: sem ele, um
// gateway que aceita a conexao e nunca responde seguraria a origem
// indefinidamente, e ela pararia de amostrar. Uma origem que trava esperando o
// gateway e pior que uma origem que acumula no buffer.
//
// configuracaoTLS nula deixa a conexao em texto claro. Ligada, ela carrega a
// credencial desta origem e a CA da instalacao — e o no passa a AUTENTICAR o
// gateway alem de se autenticar: sem isso, ele entregaria dado a qualquer um que
// atendesse naquele endereco.
func NovoTransportadorHTTP(destino string, tempoLimite time.Duration,
	configuracaoTLS *tls.Config) *TransportadorHTTP {

	transporte := http.DefaultTransport.(*http.Transport).Clone()
	transporte.TLSClientConfig = configuracaoTLS

	return &TransportadorHTTP{
		cliente: &http.Client{Timeout: tempoLimite, Transport: transporte},
		destino: destino,
	}
}

// Despachar envia a remessa e interpreta a resposta do gateway.
//
// A traducao do status HTTP para a taxonomia de falha e o espelho do mapeador do
// lado do gateway, e a distincao que importa e uma so:
//
//	4xx — o gateway nao vai aceitar isto nunca; DESCARTAR
//	5xx — o defeito e do gateway; RETRANSMITIR
//
// Confundir os dois e caro nas duas direcoes: tratar 4xx como retentavel faz a
// origem tentar para sempre e nunca avancar; tratar 5xx como definitivo faz ela
// jogar fora dado bom por causa de um problema alheio.
func (t *TransportadorHTTP) Despachar(ctx context.Context,
	remessa *contratov1.Remessa) (*contratov1.ConfirmacaoDeRemessa, error) {

	corpo, err := proto.Marshal(remessa)
	if err != nil {
		return nil, falha.Envolver(falha.CategoriaInterna, operacaoDespachar,
			"falha ao serializar a remessa", err)
	}

	requisicao, err := http.NewRequestWithContext(ctx, http.MethodPost, t.destino, bytes.NewReader(corpo))
	if err != nil {
		return nil, falha.Envolver(falha.CategoriaInterna, operacaoDespachar,
			"falha ao montar a requisicao", err)
	}
	requisicao.Header.Set("Content-Type", tipoDeConteudoProtobuf)

	resposta, err := t.cliente.Do(requisicao)
	if err != nil {
		// Gateway inalcancavel e CategoriaIndisponivel, nunca Interna: nao ha
		// defeito nem na origem nem no gateway, ha uma rede fora. A resposta certa
		// e retentar com recuo, e nao alarmar.
		return nil, falha.Envolver(falha.CategoriaIndisponivel, operacaoDespachar,
			"gateway inalcancavel", err)
	}
	defer func() { _ = resposta.Body.Close() }()

	if resposta.StatusCode == http.StatusTooManyRequests {
		espera := esperaSolicitada(resposta.Header.Get("Retry-After"))
		return nil, &Contrapressao{
			espera: espera,
			causa: falha.Nova(falha.CategoriaRecursoEsgotado, operacaoDespachar,
				"gateway saturado: ele pediu "+espera.String()),
		}
	}
	// 507 ANTES do 5xx generico, e a ordem e a correcao de um defeito real.
	//
	// A primeira versao desta versao acrescentou ArmazenamentoEsgotado a taxonomia e ao
	// mapeador do gateway, e esqueceu deste lado: o 507 caia no `>= 500` logo abaixo,
	// virava Indisponivel, e a origem o registrava como falha transitoria comum —
	// calando depois de tres linhas. A categoria existia e NUNCA era produzida no fio.
	//
	// O teste de ponta a ponta pegou, e o achado e o de sempre neste projeto: um
	// caminho que ninguem exercita e um caminho que nao funciona.
	if resposta.StatusCode == http.StatusInsufficientStorage {
		return nil, falha.Nova(falha.CategoriaArmazenamentoEsgotado, operacaoDespachar,
			"o disco do gateway esta cheio: o dado permanece nesta origem ate haver espaco")
	}
	if resposta.StatusCode >= http.StatusInternalServerError {
		return nil, falha.Nova(falha.CategoriaIndisponivel, operacaoDespachar,
			"gateway respondeu "+strconv.Itoa(resposta.StatusCode)+": retransmitir")
	}
	// 403 e recusa de IDENTIDADE, e merece categoria propria.
	//
	// Ela significa que este dispositivo esta mal configurado — o identificador que
	// ele reivindica nao e o que o certificado prova. NENHUMA remessa dele sera
	// aceita ate alguem corrigir a configuracao.
	//
	// Descartar seria errado: o dado e bom, e volta a ser aceito assim que o
	// identificador for corrigido. Retentar em silencio tambem seria errado: o
	// problema nao se resolve sozinho, e o operador precisa ver.
	if resposta.StatusCode == http.StatusForbidden {
		return nil, falha.Nova(falha.CategoriaPermissaoNegada, operacaoDespachar,
			"o gateway recusou a identidade desta origem: o identificador reivindicado nao "+
				"corresponde ao certificado. Corrija a configuracao do dispositivo")
	}
	if resposta.StatusCode >= http.StatusBadRequest {
		return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoDespachar,
			"gateway recusou a remessa com "+strconv.Itoa(resposta.StatusCode)+": descartar")
	}

	bruto, err := io.ReadAll(io.LimitReader(resposta.Body, tamanhoMaximoDaConfirmacao))
	if err != nil {
		return nil, falha.Envolver(falha.CategoriaIndisponivel, operacaoDespachar,
			"falha ao ler a confirmacao", err)
	}

	var confirmacao contratov1.ConfirmacaoDeRemessa
	if err := proto.Unmarshal(bruto, &confirmacao); err != nil {
		return nil, falha.Envolver(falha.CategoriaIndisponivel, operacaoDespachar,
			"confirmacao ilegivel", err)
	}
	return &confirmacao, nil
}

// EsperaDeRecuo devolve quanto esperar antes da proxima tentativa.
//
// Recuo exponencial com JITTER, limitado por um teto. O jitter nao e refinamento:
// sem ele, todas as origens que falharam ao mesmo tempo tentam de novo nos mesmos
// instantes, a frota inteira reconecta junta, e o gateway que estava apenas lento
// cai de vez. A falha parcial vira total.
//
// O teto existe para que uma indisponibilidade longa nao empurre a proxima
// tentativa para daqui a horas — quando o gateway voltar, a origem precisa
// perceber em segundos, nao no proximo turno.
func EsperaDeRecuo(tentativa int, base, teto time.Duration, fracaoDeJitter float64,
	sortear func() float64) time.Duration {

	espera := base
	for range tentativa {
		espera *= 2
		if espera >= teto {
			espera = teto
			break
		}
	}
	return ComJitter(espera, fracaoDeJitter, sortear)
}

// ComJitter espalha uma espera em torno do valor calculado.
//
// Extraida para ser aplicada tambem a espera que o GATEWAY pede, e nao so ao recuo
// exponencial. Ali ela e ainda mais necessaria: o gateway manda o mesmo numero para
// a frota inteira, entao sem jitter todas as origens voltariam no mesmo instante e
// o gateway que estava apenas saturado receberia um pico sincronizado.
//
// O deslocamento e simetrico: sortear() devolve [0,1), e o resultado vai de
// -fracao a +fracao em torno da espera.
func ComJitter(espera time.Duration, fracaoDeJitter float64, sortear func() float64) time.Duration {
	deslocamento := (sortear()*2 - 1) * fracaoDeJitter
	espalhada := time.Duration(float64(espera) * (1 + deslocamento))
	if espalhada < 0 {
		return 0
	}
	return espalhada
}

// Contrapressao e a recusa por saturacao, carregando a espera que o gateway MEDIU.
//
// Tipo proprio, e nao apenas uma falha classificada, porque aqui ha um DADO a
// transportar alem da categoria — e falha.Erro nao carrega dado por escolha: a
// taxonomia responde "o que fazer", nunca "com qual parametro". Envolver a falha
// em vez de estende-la mantem falha.CategoriaDe funcionando por errors.As, e
// portanto mantem a taxonomia unica sendo unica.
type Contrapressao struct {
	espera time.Duration
	causa  error
}

// Error implementa a interface error. O nome e imposto pela linguagem.
func (c *Contrapressao) Error() string { return c.causa.Error() }

// Unwrap expoe a falha classificada para errors.Is, errors.As e falha.CategoriaDe.
func (c *Contrapressao) Unwrap() error { return c.causa }

// Espera devolve quanto o gateway pediu que a origem aguardasse.
func (c *Contrapressao) Espera() time.Duration { return c.espera }

// EsperaSolicitada devolve a espera pedida pelo gateway, se houver alguma.
//
// Ausencia e um resultado legitimo, e nao um erro: um gateway mais antigo, ou um
// intermediario que corta cabecalhos, produz 429 sem Retry-After. Nesse caso a
// origem volta ao recuo exponencial — adivinhar e pior que saber, e melhor que
// nao recuar.
func EsperaSolicitada(err error) (time.Duration, bool) {
	var contrapressao *Contrapressao
	if errors.As(err, &contrapressao) && contrapressao.espera > 0 {
		return contrapressao.espera, true
	}
	return 0, false
}

// esperaSolicitada interpreta o cabecalho Retry-After.
//
// SO A FORMA EM SEGUNDOS. O HTTP admite tambem uma data absoluta, e ignora-la aqui
// nao e simplificacao: uma origem sem relogio de bateria nasce em 1970 e nao tem
// como interpretar "espere ate quinta-feira, 14h32". E a mesma limitacao que
// obrigou o gateway a servir tempo por UDP na V2.1, e ela vale aqui pelo mesmo
// motivo — um numero de segundos e a unica forma que uma origem sem hora sabe ler.
//
// Valor ausente, ilegivel ou negativo devolve zero, e zero significa "o gateway
// nao disse": a origem cai no recuo exponencial em vez de voltar imediatamente.
func esperaSolicitada(cabecalho string) time.Duration {
	if cabecalho == "" {
		return 0
	}
	segundos, err := strconv.Atoi(cabecalho)
	if err != nil || segundos <= 0 {
		return 0
	}
	return time.Duration(segundos) * time.Second
}
