// Command synkacore-no e a origem do dado — o que numa instalacao com hardware
// seria o equipamento embarcado.
//
// Ele existe porque o SynkaCore e desenvolvido software-first, e porque a V2.0
// precisava sair do impasse em que o roteiro anterior parou: as versoes seguintes
// exigiam CLP, sensores e servidor OPC UA reais, e sem esse hardware o projeto
// nao andava.
//
// A saida NAO foi simular por dentro. Um simulador ligado direto ao gateway por
// chamada de funcao — como na V1.x — deixa sem exercicio justamente o que e dificil:
// serializacao, lote, contrapressao, retransmissao, idempotencia e ancoragem de
// tempo. Este processo fala com o gateway pelo MESMO contrato de fio que um
// equipamento real usaria, e o gateway nao tem como saber a diferenca.
//
// Trocar isto por hardware deixa de ser uma integracao e passa a ser uma troca de
// quem gera os numeros.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/no"
	"github.com/ViktorWalde/SynkaCore/internal/no/simulacao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/credencial"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/identificador"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/relogio"
)

const (
	destinoPadrao = "http://127.0.0.1:8443/ingestao"

	// tempoLimiteDeDespachoPadrao limita quanto a origem espera pelo gateway.
	//
	// Sem ele, um gateway que aceita a conexao e nunca responde seguraria a origem
	// indefinidamente e ela PARARIA DE AMOSTRAR. Uma origem que trava esperando o
	// gateway e pior que uma que acumula no buffer: a segunda perde entrega, a
	// primeira perde a medicao.
	tempoLimiteDeDespachoPadrao = 10 * time.Second
)

func main() {
	if err := executar(); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "synkacore-no: %v\n", err)
		os.Exit(1)
	}
}

// montarTLS liga mTLS quando ha credencial, e recusa credencial pela metade.
//
// O nome do servidor sai do proprio destino: validar o certificado do gateway e
// confirmar que ele corresponde ao ENDERECO discado, e nao apenas que foi assinado
// pela CA. Sem isso, qualquer dispositivo da instalacao poderia se passar pelo
// gateway — ele tem certificado valido da mesma autoridade.
func montarTLS(material credencial.Material, destino string,
	registro *slog.Logger) (*tls.Config, error) {

	if !material.Algum() {
		registro.Warn("origem sem credencial: a conexao vai em texto claro e o gateway " +
			"nao e autenticado. So opere assim em rede controlada")
		return nil, nil
	}
	if !material.Completo() {
		return nil, fmt.Errorf(
			"credenciais incompletas: -ca, -certificado e -chave precisam vir juntos")
	}

	endereco, err := url.Parse(destino)
	if err != nil {
		return nil, fmt.Errorf("destino invalido: %w", err)
	}

	return credencial.ConfiguracaoDeCliente(material, endereco.Hostname())
}

func executar() error {
	padrao := no.ConfiguracaoPadrao()

	destino := flag.String("gateway", destinoPadrao, "URL do caminho de ingestao do gateway")
	dispositivo := flag.String("dispositivo", padrao.IDDoDispositivo,
		"identificador desta origem, unico na instalacao inteira")
	instalacao := flag.String("instalacao", padrao.IDDaInstalacao, "identificador da instalacao")
	intervalo := flag.Duration("intervalo", padrao.IntervaloDeAmostragem, "periodo de amostragem")
	capacidade := flag.Int("buffer", padrao.CapacidadeDoBuffer, "capacidade do buffer local, em itens")
	lote := flag.Int("lote", padrao.EnvelopesPorRemessa, "envelopes por remessa")
	semente := flag.Uint64("semente", 0,
		"semente do gerador de ruido; zero sorteia (use um valor fixo para serie reproduzivel)")
	caminhoDaCA := flag.String("ca", "", "certificado da CA da instalacao; liga mTLS")
	caminhoDoCertificado := flag.String("certificado", "", "certificado deste dispositivo")
	caminhoDaChave := flag.String("chave", "", "chave privada deste dispositivo")
	nivelDeRegistro := flag.String("registro", "info", "nivel de registro: debug, info, warn, error")
	flag.Parse()

	var severidade slog.Level
	if err := severidade.UnmarshalText([]byte(*nivelDeRegistro)); err != nil {
		severidade = slog.LevelInfo
	}
	registro := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: severidade}))

	configuracao := padrao
	configuracao.IDDoDispositivo = *dispositivo
	configuracao.IDDaInstalacao = *instalacao
	configuracao.IntervaloDeAmostragem = *intervalo
	configuracao.CapacidadeDoBuffer = *capacidade
	configuracao.EnvelopesPorRemessa = *lote

	// A sessao de boot e sorteada A CADA PARTIDA, e nunca configurada.
	//
	// E o que permite ao gateway ancorar o tempo monotonico desta execucao ao
	// relogio dele, e o que impede os numeros de sequencia — que reiniciam em zero
	// agora — de colidirem com os da partida anterior. Fixa-la por configuracao
	// faria a segunda partida ter todo o seu dado descartado como duplicata.
	configuracao.IDDaSessaoDeBoot = identificador.Sortear("boot-")

	// Nao criptografico de proposito: alimenta o ruido da simulacao e o jitter do
	// recuo. A sessao de boot, que precisa ser imprevisivel por compor a chave de
	// idempotencia, vem de identificador.Sortear, que usa crypto/rand.
	gerador := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())) //nolint:gosec // ruido e jitter
	if *semente != 0 {
		gerador = rand.New(rand.NewPCG(*semente, *semente)) //nolint:gosec // semente fixa para serie reproduzivel
	}

	configuracaoTLS, err := montarTLS(credencial.Material{
		CaminhoDaCA:          *caminhoDaCA,
		CaminhoDoCertificado: *caminhoDoCertificado,
		CaminhoDaChave:       *caminhoDaChave,
	}, *destino, registro)
	if err != nil {
		return err
	}

	ctx, encerrar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer encerrar()

	origem := no.NovoNo(
		configuracao,
		simulacao.NovaCamaraDeVacuo(gerador),
		no.NovoTransportadorHTTP(*destino, tempoLimiteDeDespachoPadrao, configuracaoTLS),
		relogio.Sistema(),
		gerador,
		registro,
	)

	registro.Info("synkacore-no iniciando",
		slog.String("id_do_dispositivo", configuracao.IDDoDispositivo),
		slog.String("id_da_sessao_de_boot", configuracao.IDDaSessaoDeBoot),
		slog.String("gateway", *destino),
		slog.Duration("intervalo_de_amostragem", configuracao.IntervaloDeAmostragem),
		slog.Int("capacidade_do_buffer", configuracao.CapacidadeDoBuffer),
		slog.Bool("autenticado", configuracaoTLS != nil))

	err = origem.Executar(ctx)
	registro.Info("synkacore-no encerrado")
	return err
}
