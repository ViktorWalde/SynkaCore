package servidordetempo_test

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/entrada/servidordetempo"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/relogio"
)

const eraUnixEmSegundosDaEraNTP = 2_208_988_800

var instanteDeReferencia = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

// servidorDeTeste sobe o servidor numa porta livre e devolve o endereco.
func servidorDeTeste(t *testing.T) (string, *relogio.Falso) {
	t.Helper()

	// Porta zero deixa o sistema escolher: testes que fixam porta colidem quando
	// rodam em paralelo, e a falha aparece como flakiness sem causa aparente.
	conexao, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("nao foi possivel achar porta livre: %v", err)
	}
	endereco := conexao.LocalAddr().String()
	_ = conexao.Close()

	falso := relogio.NovoFalso(instanteDeReferencia)
	servidor := servidordetempo.Novo(falso,
		slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})))

	ctx, cancelar := context.WithCancel(t.Context())
	t.Cleanup(cancelar)

	pronto := make(chan error, 1)
	go func() { pronto <- servidor.Escutar(ctx, endereco) }()

	// Espera curta para a porta abrir. Sem isso o primeiro pacote se perde e o teste
	// falha por corrida, nao por defeito.
	time.Sleep(50 * time.Millisecond)
	select {
	case err := <-pronto:
		t.Fatalf("servidor encerrou na partida: %v", err)
	default:
	}

	return endereco, falso
}

// consultar envia um pedido de tempo e devolve a resposta.
func consultar(t *testing.T, endereco string, pedido []byte) []byte {
	t.Helper()

	conexao, err := net.Dial("udp", endereco)
	if err != nil {
		t.Fatalf("conexao falhou: %v", err)
	}
	defer func() { _ = conexao.Close() }()

	if err := conexao.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("prazo falhou: %v", err)
	}
	if _, err := conexao.Write(pedido); err != nil {
		t.Fatalf("envio falhou: %v", err)
	}

	resposta := make([]byte, 48)
	tamanho, err := conexao.Read(resposta)
	if err != nil {
		t.Fatalf("leitura falhou: %v", err)
	}
	return resposta[:tamanho]
}

// pedidoPadrao monta uma consulta de cliente com carimbo de transmissao conhecido.
func pedidoPadrao(carimboDoCliente uint32) []byte {
	pedido := make([]byte, 48)
	pedido[0] = (4 << 3) | 3 // versao 4, modo cliente
	binary.BigEndian.PutUint32(pedido[40:44], carimboDoCliente)
	return pedido
}

// TestServidorDevolveOInstanteDoGateway e o motivo de este servico existir.
//
// Sem ele, uma origem embarcada nasce em 1970 e nao consegue validar o certificado
// do gateway — a documentacao do MicroPython diz que CERT_REQUIRED exige data e hora
// corretas. A alternativa seria o no nao autenticar o gateway, aceitando qualquer
// impostor que atenda naquele endereco.
func TestServidorDevolveOInstanteDoGateway(t *testing.T) {
	endereco, _ := servidorDeTeste(t)

	resposta := consultar(t, endereco, pedidoPadrao(0))
	if len(resposta) != 48 {
		t.Fatalf("resposta com %d bytes, esperado 48", len(resposta))
	}

	segundos := binary.BigEndian.Uint32(resposta[40:44])
	instante := time.Unix(int64(segundos)-eraUnixEmSegundosDaEraNTP, 0).UTC()

	if !instante.Equal(instanteDeReferencia) {
		t.Errorf("instante servido = %v, esperado %v", instante, instanteDeReferencia)
	}
}

// TestEraEConvertidaCorretamente protege contra o erro de setenta anos.
//
// O protocolo conta desde 1900; o tempo Unix conta desde 1970. Errar a constante
// deslocaria todo relogio da planta em sete decadas — e o sintoma seria certificado
// "ainda nao valido", que nao aponta para o relogio em lugar nenhum.
func TestEraEConvertidaCorretamente(t *testing.T) {
	endereco, _ := servidorDeTeste(t)

	segundos := binary.BigEndian.Uint32(consultar(t, endereco, pedidoPadrao(0))[40:44])
	comoUnix := int64(segundos) - eraUnixEmSegundosDaEraNTP

	if comoUnix != instanteDeReferencia.Unix() {
		t.Errorf("segundos convertidos = %d, esperado %d (diferenca de %d segundos)",
			comoUnix, instanteDeReferencia.Unix(), comoUnix-instanteDeReferencia.Unix())
	}
}

// TestRespostaEcoaOCarimboDoCliente verifica o que permite casar pedido e resposta.
//
// Sem o eco, o cliente nao tem como saber se a resposta corresponde a consulta que
// ele fez — e aceitaria uma resposta atrasada de uma consulta anterior como se fosse
// a atual.
func TestRespostaEcoaOCarimboDoCliente(t *testing.T) {
	endereco, _ := servidorDeTeste(t)
	const carimbo = 0xDEADBEEF

	resposta := consultar(t, endereco, pedidoPadrao(carimbo))

	if ecoado := binary.BigEndian.Uint32(resposta[24:28]); ecoado != carimbo {
		t.Errorf("carimbo ecoado = %#x, esperado %#x", ecoado, carimbo)
	}
}

// TestRespostaDeclaraModoServidorEVersaoDoCliente cobre o cabecalho.
func TestRespostaDeclaraModoServidorEVersaoDoCliente(t *testing.T) {
	endereco, _ := servidorDeTeste(t)

	resposta := consultar(t, endereco, pedidoPadrao(0))

	if modo := resposta[0] & 0x07; modo != 4 {
		t.Errorf("modo = %d, esperado 4 (servidor)", modo)
	}
	if versao := (resposta[0] >> 3) & 0x07; versao != 4 {
		t.Errorf("versao = %d, esperado a do cliente (4)", versao)
	}
	if resposta[1] != 1 {
		t.Errorf("estrato = %d, esperado 1", resposta[1])
	}
}

// TestPacoteCurtoNaoRecebeResposta fecha o vetor de amplificacao.
//
// Responder a qualquer coisa transformaria este servico em amplificador: um pacote
// pequeno com origem forjada produziria resposta maior enviada a vitima.
func TestPacoteCurtoNaoRecebeResposta(t *testing.T) {
	endereco, _ := servidorDeTeste(t)

	conexao, err := net.Dial("udp", endereco)
	if err != nil {
		t.Fatalf("conexao falhou: %v", err)
	}
	defer func() { _ = conexao.Close() }()

	if _, err := conexao.Write([]byte{0x23}); err != nil {
		t.Fatalf("envio falhou: %v", err)
	}
	if err := conexao.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatalf("prazo falhou: %v", err)
	}

	resposta := make([]byte, 48)
	if _, err := conexao.Read(resposta); err == nil {
		t.Error("pacote curto recebeu resposta: o servico amplificaria trafego")
	}
}

// TestOTempoServidoAcompanhaORelogioDoGateway verifica que nao ha carimbo congelado.
func TestOTempoServidoAcompanhaORelogioDoGateway(t *testing.T) {
	endereco, falso := servidorDeTeste(t)

	primeiro := binary.BigEndian.Uint32(consultar(t, endereco, pedidoPadrao(0))[40:44])
	falso.Avancar(2 * time.Hour)
	segundo := binary.BigEndian.Uint32(consultar(t, endereco, pedidoPadrao(0))[40:44])

	if diferenca := int64(segundo) - int64(primeiro); diferenca != 7200 {
		t.Errorf("o tempo servido avancou %d segundos, esperado 7200", diferenca)
	}
}
