// Package servidordetempo serve a hora aos nos da rede de chao de fabrica.
//
// POR QUE O GATEWAY PRECISA SERVIR TEMPO, se o desenho inteiro insiste que o no
// NUNCA afirma saber a hora.
//
// Sao dois usos diferentes do relogio, e confundi-los seria o erro:
//
//	PARA O DADO      — o no nao sabe a hora e nao deve saber. Ele reporta apenas
//	                   tempo monotonico desde o boot, e o gateway ancora. Isto NAO
//	                   muda, e nada aqui o afeta.
//	PARA O CERTIFICADO — validar TLS exige comparar a validade do certificado com o
//	                   instante atual. Uma origem embarcada sem relogio de bateria
//	                   nasce em 1970, e toda validacao falharia.
//
// A documentacao do MicroPython diz isso explicitamente: `ssl.CERT_REQUIRED`
// "requires the device's date/time to be properly set". Sem servir tempo, a escolha
// seria entre o no nao validar o gateway — aceitando qualquer impostor que atenda
// naquele endereco — ou nao conseguir conectar.
//
// A rede de chao de fabrica e isolada e sem internet, entao nao ha NTP publico a
// alcancar. O gateway e o unico relogio confiavel que existe ali, e ele ja precisa
// de relogio com bateria por ser a autoridade de tempo do sistema.
//
// O QUE ISSO CUSTA, dito com clareza: este servico NAO e autenticado — nao pode ser,
// porque ele existe justamente para viabilizar a autenticacao. Quem controlar a rede
// pode mentir sobre a hora e fazer um certificado vencido parecer valido. O ataque
// exige deslocar o tempo em ANOS, ja que a validade dos certificados e longa, e quem
// tem esse controle da rede tem caminhos mais diretos. Fica registrado, e nao
// escondido.
package servidordetempo

import (
	"context"
	"encoding/binary"
	"log/slog"
	"net"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/relogio"
)

const (
	// PortaPadrao e a porta do protocolo de tempo de rede.
	PortaPadrao = 123

	// tamanhoDoPacote e o tamanho fixo de um pacote SNTP.
	tamanhoDoPacote = 48

	// eraUnixEmSegundosDaEraNTP e a diferenca entre as duas epocas.
	//
	// O protocolo conta desde 1900; o tempo Unix conta desde 1970. Setenta anos, com
	// os dezessete bissextos do periodo. Errar esta constante desloca todo relogio
	// da planta em setenta anos — e o sintoma seria certificado "ainda nao valido",
	// que nao aponta para o relogio.
	eraUnixEmSegundosDaEraNTP = 2_208_988_800

	// fracaoPorSegundo converte a parte fracionaria para a escala de 32 bits do
	// protocolo.
	fracaoPorSegundo = 4_294_967_296.0

	// modoServidor e o codigo de modo que identifica a resposta.
	modoServidor = 4

	// estratoDeReferenciaPrimaria declara que este relogio nao deriva de outro.
	//
	// Estrato 1 e uma afirmacao FORTE: normalmente significa relogio atomico ou GPS.
	// Aqui ele e honesto por outro motivo — nesta rede isolada nao existe fonte
	// acima do gateway, e declarar estrato maior faria o no procurar uma referencia
	// melhor que nao existe.
	estratoDeReferenciaPrimaria = 1
)

// Servidor responde consultas de tempo dos nos.
type Servidor struct {
	relogio  relogio.Relogio
	registro *slog.Logger
	conexao  net.PacketConn
}

// Novo constroi o servidor.
func Novo(r relogio.Relogio, registro *slog.Logger) *Servidor {
	return &Servidor{relogio: r, registro: registro}
}

// Escutar abre a porta e atende ate o contexto ser cancelado.
//
// O endereco deve ser o da interface de CHAO DE FABRICA. Servir tempo ao lado de
// escritorio nao tem proposito e amplia a superficie exposta a rede que o modelo de
// ameaca trata como hostil.
func (s *Servidor) Escutar(ctx context.Context, endereco string) error {
	configuracao := net.ListenConfig{}

	conexao, err := configuracao.ListenPacket(ctx, "udp", endereco)
	if err != nil {
		return err
	}
	s.conexao = conexao

	go func() {
		<-ctx.Done()
		_ = conexao.Close()
	}()

	s.registro.Info("servidor de tempo no ar",
		slog.String("endereco", endereco),
		slog.String("proposito", "permitir que as origens validem o certificado do gateway"))

	s.atender(ctx)
	return nil
}

func (s *Servidor) atender(ctx context.Context) {
	recebido := make([]byte, tamanhoDoPacote)

	for {
		if ctx.Err() != nil {
			return
		}

		tamanho, remetente, err := s.conexao.ReadFrom(recebido)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.registro.Debug("leitura no servidor de tempo falhou", slog.String("erro", err.Error()))
			continue
		}

		// Pacote curto e descartado em silencio, sem resposta.
		//
		// Responder a qualquer coisa transformaria este servico em amplificador de
		// trafego: um pacote pequeno com origem forjada produziria uma resposta
		// maior enviada a vitima.
		if tamanho < tamanhoDoPacote {
			continue
		}

		s.responder(remetente, recebido)
	}
}

// responder monta e envia a resposta de tempo.
func (s *Servidor) responder(destinatario net.Addr, recebido []byte) {
	agora := s.relogio.Agora()
	resposta := make([]byte, tamanhoDoPacote)

	// Primeiro octeto: sem indicador de segundo intercalar, versao ecoada do pedido,
	// modo servidor. Ecoar a versao evita recusa por cliente que exige a propria.
	versaoDoCliente := (recebido[0] >> 3) & 0x07
	resposta[0] = (versaoDoCliente << 3) | modoServidor
	resposta[1] = estratoDeReferenciaPrimaria
	resposta[2] = recebido[2] // intervalo de consulta, ecoado
	resposta[3] = 0xEC        // precisao: cerca de 15 microssegundos

	// Identificador de referencia. "LOCL" declara relogio local — honesto: o gateway
	// nao deriva de fonte externa nesta rede.
	copy(resposta[12:16], []byte("LOCL"))

	// O carimbo de ORIGEM e o de transmissao do cliente, ecoado de volta. E o que
	// permite ao cliente casar a resposta com o pedido dele; sem isso, ele nao tem
	// como saber se a resposta e da consulta que fez.
	copy(resposta[24:32], recebido[40:48])

	carimbo := carimboDaEra(agora)
	copy(resposta[16:24], carimbo) // referencia
	copy(resposta[32:40], carimbo) // recepcao
	copy(resposta[40:48], carimbo) // transmissao

	if _, err := s.conexao.WriteTo(resposta, destinatario); err != nil {
		s.registro.Debug("resposta de tempo nao chegou", slog.String("erro", err.Error()))
	}
}

// carimboDaEra converte um instante para o formato de 64 bits do protocolo.
func carimboDaEra(instante time.Time) []byte {
	segundos := uint64(instante.Unix() + eraUnixEmSegundosDaEraNTP)
	fracao := uint64(float64(instante.Nanosecond()) / 1e9 * fracaoPorSegundo)

	carimbo := make([]byte, 8)
	binary.BigEndian.PutUint32(carimbo[0:4], uint32(segundos))
	binary.BigEndian.PutUint32(carimbo[4:8], uint32(fracao))
	return carimbo
}
