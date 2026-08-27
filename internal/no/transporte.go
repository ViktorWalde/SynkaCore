package no

import (
	"bytes"
	"context"
	"crypto/tls"
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
		return nil, falha.Nova(falha.CategoriaRecursoEsgotado, operacaoDespachar,
			"gateway saturado: aguardar "+resposta.Header.Get("Retry-After")+"s")
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

	// O jitter e simetrico em torno da espera calculada: sortear() devolve [0,1),
	// e o deslocamento vai de -fracao a +fracao.
	deslocamento := (sortear()*2 - 1) * fracaoDeJitter
	comJitter := time.Duration(float64(espera) * (1 + deslocamento))
	if comJitter < 0 {
		return 0
	}
	return comJitter
}
