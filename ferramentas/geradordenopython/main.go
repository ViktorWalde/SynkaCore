// Command geradordenopython emite o codificador protobuf do no em Python.
//
// POR QUE ISTO EXISTE. O invariante do projeto e que o contrato .proto e a FONTE
// UNICA da qual todas as pontas sao geradas. Para o gateway, o protoc resolve; para
// um no em MicroPython, nao ha gerador que sirva:
//
//   - uprotobuf gera do .proto, mas so fala proto2 — sem oneof e sem optional, que
//     e exatamente o que este contrato usa;
//   - minipb roda em MicroPython, mas o esquema e escrito A MAO, e um esquema
//     escrito a mao diverge do contrato sem nada acusar.
//
// A alternativa seria escrever o codificador do no a mao. Funciona no primeiro dia
// e apodrece no dia em que o contrato mudar — e a divergencia moraria num arquivo
// que nem esta neste repositorio.
//
// ESCOPO DELIBERADAMENTE PEQUENO. O no MicroPython e banco de testes; o firmware de
// producao sera C sobre ESP-IDF, onde o nanopb ja gera do mesmo .proto. Este
// emissor e descartavel, e por isso ele cobre apenas o que o no precisa: codificar
// as mensagens que ele ENVIA e decodificar a confirmacao que ele recebe. Nao e um
// gerador de protobuf de proposito geral, e nao deve virar um.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
)

// mensagensQueONoCodifica sao as que o no monta e envia.
//
// Lista explicita, e nao "todas as do arquivo", porque gerar o contrato inteiro
// levaria para a flash do ESP32 codificadores de mensagens que o no nunca emite —
// e memoria num ESP32 e o recurso que de fato falta.
var mensagensQueONoCodifica = []string{
	"EnderecoDeCanal",
	"AmostraEscalar",
	"SaudeDaOrigem",
	"DescritorDeCanal",
	"DescritorDaOrigem",
	"Envelope",
	"Remessa",
}

// mensagensQueONoDecodifica sao as que o gateway devolve.
var mensagensQueONoDecodifica = []string{
	"ConfirmacaoDeRemessa",
}

func main() {
	saida := flag.String("saida", "", "arquivo Python a escrever")
	flag.Parse()

	if *saida == "" {
		fmt.Fprintln(os.Stderr, "geradordenopython: informe -saida")
		os.Exit(1)
	}

	codigo, err := gerar()
	if err != nil {
		fmt.Fprintf(os.Stderr, "geradordenopython: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*saida, []byte(codigo), 0o644); err != nil { //nolint:gosec // codigo-fonte gerado
		fmt.Fprintf(os.Stderr, "geradordenopython: %v\n", err)
		os.Exit(1)
	}
}

func gerar() (string, error) {
	arquivo := contratov1.File_synkacore_contrato_v1_aquisicao_proto

	var codigo strings.Builder
	codigo.WriteString(preambulo)

	for _, nome := range mensagensQueONoCodifica {
		descritor := arquivo.Messages().ByName(protoreflect.Name(nome))
		if descritor == nil {
			return "", fmt.Errorf("mensagem %q nao existe no contrato", nome)
		}
		if err := emitirCodificador(&codigo, descritor); err != nil {
			return "", err
		}
	}

	for _, nome := range mensagensQueONoDecodifica {
		descritor := arquivo.Messages().ByName(protoreflect.Name(nome))
		if descritor == nil {
			return "", fmt.Errorf("mensagem %q nao existe no contrato", nome)
		}
		emitirDecodificador(&codigo, descritor)
	}

	codigo.WriteString(emitirEnums(arquivo))
	return codigo.String(), nil
}

// emitirCodificador escreve a funcao que serializa uma mensagem.
//
// Os campos saem em ordem de NUMERO, e nao de declaracao. Protobuf nao exige ordem,
// mas emitir sempre na mesma torna a saida deterministica — e e isso que permite ao
// teste de fidelidade comparar bytes com o codificador do Go.
func emitirCodificador(codigo *strings.Builder, descritor protoreflect.MessageDescriptor) error {
	campos := camposOrdenados(descritor)

	fmt.Fprintf(codigo, "\n\ndef codificar_%s(campos):\n", descritor.Name())
	fmt.Fprintf(codigo, "    \"\"\"Serializa %s. `campos` e um dict; chave ausente e campo omitido.\"\"\"\n",
		descritor.Name())
	codigo.WriteString("    saida = bytearray()\n")

	for _, campo := range campos {
		nome := string(campo.Name())
		fmt.Fprintf(codigo, "\n    valor = campos.get(%q)\n", nome)
		codigo.WriteString("    if valor is not None:\n")

		if campo.IsList() {
			if campo.Kind() != protoreflect.MessageKind {
				return fmt.Errorf("campo repetido %q nao e mensagem: nao suportado", nome)
			}
			codigo.WriteString("        for item in valor:\n")
			fmt.Fprintf(codigo, "            _bytes = codificar_%s(item)\n", campo.Message().Name())
			fmt.Fprintf(codigo, "            saida += _tag(%d, 2) + _varint(len(_bytes)) + _bytes\n",
				campo.Number())
			continue
		}

		linha, err := emitirCampoSimples(campo)
		if err != nil {
			return err
		}
		codigo.WriteString(linha)
	}

	codigo.WriteString("\n    return bytes(saida)\n")
	return nil
}

// emitirCampoSimples devolve a linha Python que serializa um campo nao repetido.
func emitirCampoSimples(campo protoreflect.FieldDescriptor) (string, error) {
	numero := campo.Number()

	switch campo.Kind() {
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind,
		protoreflect.Int32Kind, protoreflect.Int64Kind, protoreflect.EnumKind:
		return fmt.Sprintf("        saida += _tag(%d, 0) + _varint(valor)\n", numero), nil

	case protoreflect.BoolKind:
		return fmt.Sprintf("        saida += _tag(%d, 0) + _varint(1 if valor else 0)\n", numero), nil

	case protoreflect.FloatKind:
		return fmt.Sprintf("        saida += _tag(%d, 5) + _float32(valor)\n", numero), nil

	case protoreflect.StringKind:
		return fmt.Sprintf(
			"        _bytes = valor.encode('utf-8')\n"+
				"        saida += _tag(%d, 2) + _varint(len(_bytes)) + _bytes\n", numero), nil

	case protoreflect.MessageKind:
		return fmt.Sprintf(
			"        _bytes = codificar_%s(valor)\n"+
				"        saida += _tag(%d, 2) + _varint(len(_bytes)) + _bytes\n",
			campo.Message().Name(), numero), nil

	// Tipos que o contrato NAO usa hoje, listados explicitamente em vez de caírem
	// num retorno genérico.
	//
	// Listar tem um propósito: quem lê sabe exatamente o que o gerador cobre, e o
	// dia em que alguém acrescentar um `bytes` ou um `sint32` ao contrato, o erro
	// diz o que fazer em vez de dizer que algo deu errado. Sem isso, o gerador
	// falharia com uma mensagem que não distingue "tipo novo no protobuf" de
	// "esqueci um caso".
	case protoreflect.BytesKind, protoreflect.DoubleKind,
		protoreflect.Sint32Kind, protoreflect.Sint64Kind,
		protoreflect.Fixed32Kind, protoreflect.Fixed64Kind,
		protoreflect.Sfixed32Kind, protoreflect.Sfixed64Kind,
		protoreflect.GroupKind:
		return "", fmt.Errorf(
			"campo %q e do tipo %v, que o gerador do no ainda nao emite. "+
				"Acrescente o caso em emitirCampoSimples e a primitiva correspondente no preambulo",
			campo.Name(), campo.Kind())
	}

	return "", fmt.Errorf("campo %q tem tipo %v desconhecido pelo gerador",
		campo.Name(), campo.Kind())
}

// emitirDecodificador escreve a funcao que interpreta a resposta do gateway.
//
// Decodificador GENERICO por numero de campo, e nao um analisador completo: o no so
// precisa de tres valores da confirmacao, e um decodificador de proposito geral em
// MicroPython custaria memoria que o ESP32 nao tem sobrando.
//
// Campos desconhecidos sao PULADOS, nao recusados. E o que permite ao gateway
// acrescentar informacao a confirmacao sem quebrar nos em campo.
func emitirDecodificador(codigo *strings.Builder, descritor protoreflect.MessageDescriptor) {
	campos := camposOrdenados(descritor)

	fmt.Fprintf(codigo, "\n\ndef decodificar_%s(dados):\n", descritor.Name())
	fmt.Fprintf(codigo, "    \"\"\"Interpreta %s. Campos desconhecidos sao pulados.\"\"\"\n",
		descritor.Name())
	codigo.WriteString("    resultado = {}\n    posicao = 0\n")
	codigo.WriteString("    while posicao < len(dados):\n")
	codigo.WriteString("        chave, posicao = _ler_varint(dados, posicao)\n")
	codigo.WriteString("        numero, tipo = chave >> 3, chave & 0x07\n")
	codigo.WriteString("        valor, posicao = _ler_valor(dados, posicao, tipo)\n")

	for indice, campo := range campos {
		condicao := "        if"
		if indice > 0 {
			condicao = "        elif"
		}
		fmt.Fprintf(codigo, "%s numero == %d:\n", condicao, campo.Number())

		if campo.IsList() {
			if campo.Kind() == protoreflect.MessageKind {
				fmt.Fprintf(codigo, "            resultado.setdefault(%q, []).append(valor)\n", campo.Name())
				continue
			}

			// Escalar repetido em proto3 vem EMPACOTADO: os valores viajam juntos num
			// unico campo length-delimited, e nao um por tag.
			//
			// Tratar cada ocorrencia como valor solto — o que a primeira versao deste
			// gerador fazia — devolveria um blob opaco em vez da lista. No caso das
			// sequencias rejeitadas isso e grave e silencioso: o no nunca descartaria
			// os envelopes recusados e os retransmitiria para sempre.
			//
			// A forma nao empacotada continua sendo aceita porque o contrato aceita
			// todas as versoes ja publicadas, e um gateway antigo poderia emiti-la.
			fmt.Fprintf(codigo, "            _lista = resultado.setdefault(%q, [])\n", campo.Name())
			codigo.WriteString("            if tipo == 2:\n")
			codigo.WriteString("                _lista.extend(_ler_varints(valor))\n")
			codigo.WriteString("            else:\n")
			codigo.WriteString("                _lista.append(valor)\n")
			continue
		}
		fmt.Fprintf(codigo, "            resultado[%q] = valor\n", campo.Name())
	}

	codigo.WriteString("    return resultado\n")
}

// emitirEnums escreve as constantes dos enums que o no usa.
//
// Emitidos a partir do descritor, e nao escritos a mao, para que acrescentar uma
// grandeza ao contrato a torne disponivel no no automaticamente — o mesmo motivo
// pelo qual a configuracao da instalacao le o enum por reflexao.
func emitirEnums(arquivo protoreflect.FileDescriptor) string {
	var codigo strings.Builder

	for indice := range arquivo.Enums().Len() {
		enum := arquivo.Enums().Get(indice)
		if enum.Name() != "Grandeza" && enum.Name() != "EstadoDeMaquina" {
			continue
		}

		fmt.Fprintf(&codigo, "\n\n# %s — gerado do contrato.\n", enum.Name())
		for valorIndice := range enum.Values().Len() {
			valor := enum.Values().Get(valorIndice)
			fmt.Fprintf(&codigo, "%s = %d\n", valor.Name(), valor.Number())
		}
	}
	return codigo.String()
}

// camposOrdenados devolve os campos por numero, incluindo os de dentro de oneof.
func camposOrdenados(descritor protoreflect.MessageDescriptor) []protoreflect.FieldDescriptor {
	campos := make([]protoreflect.FieldDescriptor, 0, descritor.Fields().Len())
	for indice := range descritor.Fields().Len() {
		campos = append(campos, descritor.Fields().Get(indice))
	}
	sort.Slice(campos, func(primeiro, segundo int) bool {
		return campos[primeiro].Number() < campos[segundo].Number()
	})
	return campos
}
