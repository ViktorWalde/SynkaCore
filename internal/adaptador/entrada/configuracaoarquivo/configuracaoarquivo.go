// Package configuracaoarquivo le a configuracao da instalacao de um arquivo YAML.
//
// YAML, e nao JSON, por uma razao que decide sozinha: este arquivo e editado por
// uma PESSOA — hoje por quem desenvolve, amanha por um tecnico comissionando um
// painel — e JSON nao aceita comentario. Um mapeamento de canal para ponto de
// medicao sem espaco para explicar o que cada canal e seria ilegivel na segunda
// vez que alguem o abrisse.
//
// A decodificacao e ESTRITA: campo desconhecido derruba o carregamento. Um erro de
// digitacao em `unidade` viraria, num decodificador tolerante, um ponto de medicao
// sem unidade — e o gateway subiria com dado em escala indefinida. Falhar na
// partida com "campo desconhecido: unidad" e infinitamente melhor.
package configuracaoarquivo

import (
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/instalacao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/pontodemedicao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const (
	operacaoCarregar = "configuracaoarquivo.Carregar"

	// tamanhoMaximoDoArquivo limita o que se aceita ler do disco.
	//
	// Uma instalacao realista tem dezenas a poucas centenas de canais, o que cabe
	// em alguns quilobytes. O limite fecha o caso de alguem apontar o gateway para
	// o arquivo errado — um dump de banco, por exemplo — e ele tentar carrega-lo
	// inteiro na memoria antes de descobrir que nao e YAML.
	tamanhoMaximoDoArquivo = 4 << 20
)

// arquivo espelha a estrutura do YAML.
//
// Tipo proprio, separado do dominio, de proposito: ele carrega a forma do ARQUIVO,
// com strings e ponteiros onde o dominio tem tipos validados. Se o dominio tivesse
// tags de YAML, a forma do arquivo passaria a restringir o desenho do dominio — e
// mudar o formato do arquivo exigiria mexer nas regras.
type arquivo struct {
	Instalacao string             `yaml:"instalacao"`
	Motivos    *catalogoDeMotivos `yaml:"motivos_de_parada"`
	Pontos     []pontoDoArquivo   `yaml:"pontos_de_medicao"`
}

type catalogoDeMotivos struct {
	Versao  uint32            `yaml:"versao"`
	Codigos map[uint32]string `yaml:"codigos"`
}

type pontoDoArquivo struct {
	Dispositivo string   `yaml:"dispositivo"`
	Modulo      uint32   `yaml:"modulo"`
	Canal       uint32   `yaml:"canal"`
	Ponto       string   `yaml:"ponto"`
	Grandeza    string   `yaml:"grandeza"`
	Unidade     string   `yaml:"unidade"`
	FaixaMinima *float64 `yaml:"faixa_minima"`
	FaixaMaxima *float64 `yaml:"faixa_maxima"`
}

// Carregar le e valida a configuracao da instalacao.
//
// Toda falha aqui acontece na PARTIDA do gateway, nunca em operacao. Configuracao
// invalida derruba o processo com mensagem clara em vez de produzir dado errado em
// silencio — a mesma regra que a V1.x aplicava com validacao de settings no
// startup, e que continua valendo.
func Carregar(caminho string) (*instalacao.Instalacao, error) {
	informacao, err := os.Stat(caminho)
	if err != nil {
		return nil, falha.Envolver(falha.CategoriaNaoEncontrado, operacaoCarregar,
			"nao foi possivel abrir a configuracao da instalacao em "+caminho, err)
	}
	if informacao.Size() > tamanhoMaximoDoArquivo {
		return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoCarregar,
			"arquivo de configuracao excede o tamanho maximo: "+caminho)
	}

	bruto, err := os.ReadFile(caminho) //nolint:gosec // caminho vem da linha de comando do operador
	if err != nil {
		return nil, falha.Envolver(falha.CategoriaInterna, operacaoCarregar,
			"falha ao ler a configuracao da instalacao", err)
	}

	decodificador := yaml.NewDecoder(strings.NewReader(string(bruto)))

	// KnownFields e o que torna a decodificacao estrita. Sem ele, `unidad: Cel`
	// seria silenciosamente ignorado e o ponto ficaria sem unidade.
	decodificador.KnownFields(true)

	var doArquivo arquivo
	if err := decodificador.Decode(&doArquivo); err != nil {
		return nil, falha.Envolver(falha.CategoriaEntradaInvalida, operacaoCarregar,
			"configuracao da instalacao malformada", err)
	}

	return montar(doArquivo)
}

// montar converte a forma do arquivo para o dominio, validando cada campo.
func montar(doArquivo arquivo) (*instalacao.Instalacao, error) {
	pontos := make(map[instalacao.ChaveDeCanal]instalacao.PontoConfigurado, len(doArquivo.Pontos))

	for indice, doPonto := range doArquivo.Pontos {
		// O indice entra na mensagem porque o erro pode ser numa lista de duzentos
		// canais, e "dispositivo invalido" sem posicao obriga quem le a caçar.
		ondeEstou := "ponto_de_medicao[" + strconv.Itoa(indice) + "]"

		dispositivo, err := identidadededispositivo.AnalisarIDDoDispositivo(doPonto.Dispositivo)
		if err != nil {
			return nil, falha.Envolver(falha.CategoriaEntradaInvalida, operacaoCarregar,
				ondeEstou+": dispositivo invalido", err)
		}
		ponto, err := pontodemedicao.AnalisarIDDoPontoDeMedicao(doPonto.Ponto)
		if err != nil {
			return nil, falha.Envolver(falha.CategoriaEntradaInvalida, operacaoCarregar,
				ondeEstou+": ponto de medicao invalido", err)
		}
		grandeza, err := instalacao.AnalisarGrandeza(doPonto.Grandeza)
		if err != nil {
			return nil, falha.Envolver(falha.CategoriaEntradaInvalida, operacaoCarregar,
				ondeEstou+" ("+doPonto.Ponto+")", err)
		}

		canal := instalacao.ChaveDeCanal{
			Dispositivo: dispositivo,
			Endereco: aquisicao.EnderecoDeCanal{
				IndiceDoModulo: doPonto.Modulo,
				IndiceDoCanal:  doPonto.Canal,
			},
		}

		// Dois pontos no mesmo canal e ambiguidade que o mapa esconderia: o segundo
		// sobrescreveria o primeiro em silencio, e a instalacao rodaria medindo
		// outra coisa sem que nada acusasse.
		if anterior, repetido := pontos[canal]; repetido {
			return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoCarregar,
				ondeEstou+": o canal "+canal.String()+" ja esta configurado para o ponto "+
					anterior.Ponto.String())
		}

		pontos[canal] = instalacao.PontoConfigurado{
			Ponto:       ponto,
			Grandeza:    grandeza,
			Unidade:     strings.TrimSpace(doPonto.Unidade),
			FaixaMinima: doPonto.FaixaMinima,
			FaixaMaxima: doPonto.FaixaMaxima,
		}
	}

	parametros := instalacao.ParametrosDeInstalacao{
		ID:     strings.TrimSpace(doArquivo.Instalacao),
		Pontos: pontos,
	}
	if doArquivo.Motivos != nil {
		parametros.Motivos = doArquivo.Motivos.Codigos
		parametros.VersaoDosMotivos = doArquivo.Motivos.Versao
	}

	return instalacao.NovaInstalacao(parametros)
}
