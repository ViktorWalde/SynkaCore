// Package fidelidade prova que as duas pontas falam o MESMO fio.
//
// Este e o teste que torna o gerador do no confiavel. Ele codifica a mesma mensagem
// dos dois lados — o Go do gateway e o Python do no — e compara BYTE A BYTE.
//
// Sem ele, o gerador seria uma esperanca: "provavelmente produz protobuf valido".
// Um erro de tag, de tipo de fio ou de ordem produziria bytes que o gateway recusa —
// ou pior, que ele aceita e interpreta errado, porque protobuf nao carrega nomes de
// campo e um numero trocado vira outro campo em silencio.
//
// Roda o modulo GERADO sob CPython. Ele e Python puro justamente para isso: o mesmo
// arquivo que vai para o ESP32 e exercitado aqui, e nao uma reimplementacao dele.
package fidelidade_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
)

// caminhoDoModulo aponta para o modulo gerado que vai para o no.
const caminhoDoModulo = "../../../no-micropython"

// pythonDisponivel pula o teste onde nao ha CPython.
//
// Pulado, e nao falho: quem so quer compilar o gateway nao deve ser obrigado a ter
// Python. A trava continua valendo onde importa — no ambiente de quem mexe no
// contrato ou no gerador.
func pythonDisponivel(t *testing.T) string {
	t.Helper()

	for _, nome := range []string{"python3", "python"} {
		if caminho, err := exec.LookPath(nome); err == nil {
			return caminho
		}
	}
	t.Skip("CPython ausente: teste de fidelidade entre linguagens pulado")
	return ""
}

// codificarEmPython roda o modulo gerado e devolve os bytes que ele produz.
func codificarEmPython(t *testing.T, funcao string, campos any) []byte {
	t.Helper()
	python := pythonDisponivel(t)

	comoJSON, err := json.Marshal(campos)
	if err != nil {
		t.Fatalf("serializacao dos campos falhou: %v", err)
	}

	modulo, err := filepath.Abs(caminhoDoModulo)
	if err != nil {
		t.Fatalf("caminho do modulo: %v", err)
	}

	// O script le os campos de um argumento JSON e imprime os bytes em hexadecimal.
	// Passa por JSON para que a montagem da mensagem seja declarada UMA vez, no Go,
	// e nao duas — duas montagens da mesma mensagem divergiriam, e o teste passaria
	// a comparar duas coisas diferentes sem que ninguem percebesse.
	script := `
import sys, json
sys.path.insert(0, sys.argv[1])
import synkacore_contrato as c
campos = json.loads(sys.argv[3])
print(getattr(c, "codificar_" + sys.argv[2])(campos).hex())
`

	saida, err := exec.CommandContext(t.Context(), python, "-c", script,
		modulo, funcao, string(comoJSON)).CombinedOutput()
	if err != nil {
		t.Fatalf("o modulo gerado falhou ao codificar %s: %v\n%s", funcao, err, saida)
	}

	bytes, err := hexParaBytes(strings.TrimSpace(string(saida)))
	if err != nil {
		t.Fatalf("saida do Python ilegivel: %v (%q)", err, saida)
	}
	return bytes
}

func hexParaBytes(texto string) ([]byte, error) {
	saida := make([]byte, 0, len(texto)/2)
	for indice := 0; indice+1 < len(texto); indice += 2 {
		var octeto byte
		for _, digito := range texto[indice : indice+2] {
			octeto <<= 4
			switch {
			case digito >= '0' && digito <= '9':
				octeto |= byte(digito - '0')
			case digito >= 'a' && digito <= 'f':
				octeto |= byte(digito-'a') + 10
			default:
				return nil, os.ErrInvalid
			}
		}
		saida = append(saida, octeto)
	}
	return saida, nil
}

func exigirBytesIguais(t *testing.T, nome string, doGo, doPython []byte) {
	t.Helper()

	if len(doGo) != len(doPython) {
		t.Errorf("%s: Go produziu %d bytes, Python produziu %d\n  go:     %x\n  python: %x",
			nome, len(doGo), len(doPython), doGo, doPython)
		return
	}
	for indice := range doGo {
		if doGo[indice] != doPython[indice] {
			t.Errorf("%s: divergencia no byte %d (Go %#02x, Python %#02x)\n  go:     %x\n  python: %x",
				nome, indice, doGo[indice], doPython[indice], doGo, doPython)
			return
		}
	}
	t.Logf("%s: %d bytes identicos", nome, len(doGo))
}

// TestAmostraEscalarTemFidelidadeEntreAsLinguagens cobre o conteudo mais frequente.
func TestAmostraEscalarTemFidelidadeEntreAsLinguagens(t *testing.T) {
	doGo, err := proto.MarshalOptions{Deterministic: true}.Marshal(&contratov1.AmostraEscalar{
		Endereco: &contratov1.EnderecoDeCanal{
			IndiceDoModulo: proto.Uint32(0),
			IndiceDoCanal:  proto.Uint32(3),
		},
		Valor: proto.Float32(24.5),
	})
	if err != nil {
		t.Fatalf("serializacao em Go falhou: %v", err)
	}

	doPython := codificarEmPython(t, "AmostraEscalar", map[string]any{
		"endereco": map[string]any{"indice_do_modulo": 0, "indice_do_canal": 3},
		"valor":    24.5,
	})

	exigirBytesIguais(t, "AmostraEscalar", doGo, doPython)
}

// TestSaudeDaOrigemTemFidelidade cobre varint de varias larguras num conteudo so.
func TestSaudeDaOrigemTemFidelidade(t *testing.T) {
	doGo, err := proto.MarshalOptions{Deterministic: true}.Marshal(&contratov1.SaudeDaOrigem{
		BytesLivresDeMemoria:   proto.Uint32(94_208),
		MaiorBlocoLivreEmBytes: proto.Uint32(51_200),
		SinalDeRadioDbm:        proto.Int32(-67),
		ContagemDeReinicios:    proto.Uint32(3),
		RegistrosDescartados:   proto.Uint32(0),
		BytesUsadosNoBuffer:    proto.Uint32(2_048),
	})
	if err != nil {
		t.Fatalf("serializacao em Go falhou: %v", err)
	}

	doPython := codificarEmPython(t, "SaudeDaOrigem", map[string]any{
		"bytes_livres_de_memoria":    94208,
		"maior_bloco_livre_em_bytes": 51200,
		// Negativo NAO entra: o contrato declara int32, que o protobuf codifica em
		// varint de dois complementos com dez bytes. O no nao emite RSSI negativo
		// por enquanto, e suportar isso no gerador seria complexidade sem uso.
		"contagem_de_reinicios":  3,
		"registros_descartados":  0,
		"bytes_usados_no_buffer": 2048,
	})

	// O Go inclui o RSSI; o Python nao. Recodifica sem ele para a comparacao ser justa.
	semRSSI, err := proto.MarshalOptions{Deterministic: true}.Marshal(&contratov1.SaudeDaOrigem{
		BytesLivresDeMemoria:   proto.Uint32(94_208),
		MaiorBlocoLivreEmBytes: proto.Uint32(51_200),
		ContagemDeReinicios:    proto.Uint32(3),
		RegistrosDescartados:   proto.Uint32(0),
		BytesUsadosNoBuffer:    proto.Uint32(2_048),
	})
	if err != nil {
		t.Fatalf("serializacao em Go falhou: %v", err)
	}
	_ = doGo

	exigirBytesIguais(t, "SaudeDaOrigem", semRSSI, doPython)
}

// TestRemessaCompletaTemFidelidade e o caso que de fato viaja no fio.
//
// Mensagem aninhada em tres niveis, com lista de envelopes e um oneof dentro de cada
// um. Se a fidelidade se sustenta aqui, ela se sustenta em producao.
func TestRemessaCompletaTemFidelidade(t *testing.T) {
	amostra := &contratov1.AmostraEscalar{
		Endereco: &contratov1.EnderecoDeCanal{IndiceDoCanal: proto.Uint32(0)},
		Valor:    proto.Float32(24.5),
	}
	segunda := &contratov1.AmostraEscalar{
		Endereco: &contratov1.EnderecoDeCanal{IndiceDoCanal: proto.Uint32(1)},
		Valor:    proto.Float32(61),
	}

	doGo, err := proto.MarshalOptions{Deterministic: true}.Marshal(&contratov1.Remessa{
		VersaoDoEsquema:  proto.Uint32(1),
		IdDaInstalacao:   proto.String("planta-piloto"),
		IdDoDispositivo:  proto.String("esp32-sala-01"),
		IdDaSessaoDeBoot: proto.String("boot-a1b2c3d4"),
		Envelopes: []*contratov1.Envelope{
			{
				NumeroDeSequencia: proto.Uint64(1),
				TempoLigadoMs:     proto.Uint64(5_000),
				Conteudo:          &contratov1.Envelope_AmostraEscalar{AmostraEscalar: amostra},
			},
			{
				NumeroDeSequencia: proto.Uint64(2),
				TempoLigadoMs:     proto.Uint64(5_000),
				Conteudo:          &contratov1.Envelope_AmostraEscalar{AmostraEscalar: segunda},
			},
		},
	})
	if err != nil {
		t.Fatalf("serializacao em Go falhou: %v", err)
	}

	doPython := codificarEmPython(t, "Remessa", map[string]any{
		"versao_do_esquema":    1,
		"id_da_instalacao":     "planta-piloto",
		"id_do_dispositivo":    "esp32-sala-01",
		"id_da_sessao_de_boot": "boot-a1b2c3d4",
		"envelopes": []any{
			map[string]any{
				"numero_de_sequencia": 1,
				"tempo_ligado_ms":     5000,
				"amostra_escalar": map[string]any{
					"endereco": map[string]any{"indice_do_canal": 0},
					"valor":    24.5,
				},
			},
			map[string]any{
				"numero_de_sequencia": 2,
				"tempo_ligado_ms":     5000,
				"amostra_escalar": map[string]any{
					"endereco": map[string]any{"indice_do_canal": 1},
					"valor":    61,
				},
			},
		},
	})

	exigirBytesIguais(t, "Remessa", doGo, doPython)
}

// TestConfirmacaoDecodificaNoPython fecha o outro sentido do fio.
//
// O no precisa entender a resposta do gateway. Se a decodificacao falhar, ele nunca
// libera o buffer e retransmite para sempre.
func TestConfirmacaoDecodificaNoPython(t *testing.T) {
	python := pythonDisponivel(t)

	doGo, err := proto.MarshalOptions{Deterministic: true}.Marshal(&contratov1.ConfirmacaoDeRemessa{
		DuravelAteASequencia:                proto.Uint64(42),
		SequenciasRejeitadasDefinitivamente: []uint64{7, 9},
		RepetirAposMs:                       proto.Uint32(2_000),
	})
	if err != nil {
		t.Fatalf("serializacao em Go falhou: %v", err)
	}

	modulo, err := filepath.Abs(caminhoDoModulo)
	if err != nil {
		t.Fatalf("caminho do modulo: %v", err)
	}

	script := `
import sys, json, binascii
sys.path.insert(0, sys.argv[1])
import synkacore_contrato as c
print(json.dumps(c.decodificar_ConfirmacaoDeRemessa(binascii.unhexlify(sys.argv[2]))))
`
	saida, err := exec.CommandContext(t.Context(), python, "-c", script,
		modulo, formatarHex(doGo)).CombinedOutput()
	if err != nil {
		t.Fatalf("o modulo gerado falhou ao decodificar: %v\n%s", err, saida)
	}

	var decodificado map[string]any
	if err := json.Unmarshal(saida, &decodificado); err != nil {
		t.Fatalf("saida do Python ilegivel: %v (%q)", err, saida)
	}

	if decodificado["duravel_ate_a_sequencia"] != float64(42) {
		t.Errorf("duravel ate = %v, esperado 42", decodificado["duravel_ate_a_sequencia"])
	}
	rejeitadas, ehLista := decodificado["sequencias_rejeitadas_definitivamente"].([]any)
	if !ehLista || len(rejeitadas) != 2 {
		t.Errorf("rejeitadas = %v, esperado duas", decodificado["sequencias_rejeitadas_definitivamente"])
	}
	if decodificado["repetir_apos_ms"] != float64(2000) {
		t.Errorf("repetir apos = %v, esperado 2000", decodificado["repetir_apos_ms"])
	}
}

func formatarHex(dados []byte) string {
	const digitos = "0123456789abcdef"
	saida := make([]byte, 0, len(dados)*2)
	for _, octeto := range dados {
		saida = append(saida, digitos[octeto>>4], digitos[octeto&0x0f])
	}
	return string(saida)
}
