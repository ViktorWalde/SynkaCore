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
	"time"

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
	Admissao   *admissaoDoArquivo `yaml:"admissao"`
}

// admissaoDoArquivo e o bloco opcional que declara quanto cada classe tolera
// esperar na porta do gateway.
//
// Ponteiro, e nao valor: ausente significa "valem os padroes", e um valor zerado
// significaria "esperar zero" — que recusaria tudo. A diferenca entre "nao declarei"
// e "declarei zero" precisa sobreviver a decodificacao.
//
// Duracoes em TEXTO, interpretadas por time.ParseDuration: `2s`, `500ms`, `1m30s`.
// Um numero solto obrigaria a escolher uma unidade implicita, e `2` significando
// segundos num campo e milissegundos noutro e como um arquivo de configuracao
// produz dado errado sem ninguem digitar nada errado.
type admissaoDoArquivo struct {
	EsperaMaximaDaAmostra string `yaml:"espera_maxima_da_amostra"`
	EsperaMaximaDoEvento  string `yaml:"espera_maxima_do_evento"`
	FilaMaxima            *int   `yaml:"fila_maxima"`
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

	// Vigencia, ambas opcionais. Ausentes significam "desde sempre" e "ainda
	// aberto", que e o caso comum de uma instalacao que nunca trocou nada.
	//
	// Texto, e nao time.Time: o YAML converteria `2026-08-27` sozinho, mas em UTC
	// e sem dizer que fez isso. Interpretando aqui, a ambiguidade de fuso fica
	// explicita e documentada em vez de silenciosa.
	VigenteDe  string `yaml:"vigente_de"`
	VigenteAte string `yaml:"vigente_ate"`
}

// formatoDeDataSimples aceita a forma que um tecnico escreve naturalmente.
const formatoDeDataSimples = "2006-01-02"

// analisarVigencia interpreta uma data da configuracao.
//
// Aceita duas formas, e a diferenca entre elas importa:
//
//	2026-08-27                   — dia, interpretado como meia-noite UTC
//	2026-08-27T14:30:00-03:00    — instante exato, com fuso declarado
//
// A forma simples e ambigua por natureza: "trocamos o sensor no dia 27" quer dizer
// horario LOCAL, e a planta esta em UTC-3. Um mapeamento que comeca a meia-noite
// UTC vale a partir das 21h do dia anterior no horario local.
//
// Isso e aceitavel porque troca de sensor e evento de manutencao, nao de segundo —
// mas nao pode ficar implicito, e por isso a forma precisa existe e esta
// documentada. E o mesmo problema de fronteira de fuso que ainda esta em aberto
// para relatorio por turno.
func analisarVigencia(bruto, ondeEstou, qual string) (time.Time, error) {
	bruto = strings.TrimSpace(bruto)
	if bruto == "" {
		return time.Time{}, nil
	}

	if instante, err := time.Parse(time.RFC3339, bruto); err == nil {
		return instante.UTC(), nil
	}
	if dia, err := time.Parse(formatoDeDataSimples, bruto); err == nil {
		return dia.UTC(), nil
	}

	return time.Time{}, falha.Nova(falha.CategoriaEntradaInvalida, operacaoCarregar,
		ondeEstou+": "+qual+" invalido: "+bruto+
			". Use 2026-08-27 (meia-noite UTC) ou 2026-08-27T14:30:00-03:00 (instante exato)")
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
	// Uma LISTA por canal: o mesmo canal pode ter alimentado pontos diferentes em
	// periodos diferentes, e essa e justamente a forma de registrar troca de sensor.
	mapeamentos := make(map[instalacao.ChaveDeCanal][]instalacao.PontoConfigurado, len(doArquivo.Pontos))

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

		vigenteDe, err := analisarVigencia(doPonto.VigenteDe, ondeEstou, "vigente_de")
		if err != nil {
			return nil, err
		}
		vigenteAte, err := analisarVigencia(doPonto.VigenteAte, ondeEstou, "vigente_ate")
		if err != nil {
			return nil, err
		}

		canal := instalacao.ChaveDeCanal{
			Dispositivo: dispositivo,
			Endereco: aquisicao.EnderecoDeCanal{
				IndiceDoModulo: doPonto.Modulo,
				IndiceDoCanal:  doPonto.Canal,
			},
		}

		// Entradas repetidas para o mesmo canal NAO sao mais erro aqui: elas sao a
		// forma de registrar troca de sensor. Quem recusa sobreposicao de vigencia e
		// NovaInstalacao, que ve a lista inteira — verificar aqui, entrada por
		// entrada, seria a mesma regra em dois lugares, e duas checagens do mesmo
		// conceito divergem.
		mapeamentos[canal] = append(mapeamentos[canal], instalacao.PontoConfigurado{
			Ponto:       ponto,
			Grandeza:    grandeza,
			Unidade:     strings.TrimSpace(doPonto.Unidade),
			FaixaMinima: doPonto.FaixaMinima,
			FaixaMaxima: doPonto.FaixaMaxima,
			VigenteDe:   vigenteDe,
			VigenteAte:  vigenteAte,
		})
	}

	admissao, err := montarAdmissao(doArquivo.Admissao)
	if err != nil {
		return nil, err
	}

	parametros := instalacao.ParametrosDeInstalacao{
		ID:          strings.TrimSpace(doArquivo.Instalacao),
		Mapeamentos: mapeamentos,
		Admissao:    admissao,
	}
	if doArquivo.Motivos != nil {
		parametros.Motivos = doArquivo.Motivos.Codigos
		parametros.VersaoDosMotivos = doArquivo.Motivos.Versao
	}

	return instalacao.NovaInstalacao(parametros)
}

// montarAdmissao interpreta o bloco opcional de admissao.
//
// Bloco ausente devolve nil, e NovaInstalacao aplica os padroes. Bloco PRESENTE mas
// incompleto herda o padrao campo a campo: declarar apenas
// `espera_maxima_da_amostra` e uma intencao legitima — mexer num numero sem
// precisar repetir os outros dois e arriscar copiar errado o que nao se queria
// mudar.
//
// A coerencia entre os campos NAO e verificada aqui, e sim em instalacao.NovaAdmissao.
// Verificar nos dois lugares seria a mesma regra em duas copias, e a que ninguem
// lembra de atualizar e sempre a que esta mais longe do dominio.
func montarAdmissao(doArquivo *admissaoDoArquivo) (*instalacao.Admissao, error) {
	if doArquivo == nil {
		return nil, nil
	}

	politica := instalacao.AdmissaoPadrao()

	amostra, err := analisarDuracao(doArquivo.EsperaMaximaDaAmostra, "espera_maxima_da_amostra")
	if err != nil {
		return nil, err
	}
	if amostra > 0 {
		politica.OrcamentoDaAmostra = amostra
	}

	evento, err := analisarDuracao(doArquivo.EsperaMaximaDoEvento, "espera_maxima_do_evento")
	if err != nil {
		return nil, err
	}
	if evento > 0 {
		politica.OrcamentoDoEventoDiscreto = evento
	}

	if doArquivo.FilaMaxima != nil {
		politica.FilaMaxima = *doArquivo.FilaMaxima
	}
	return &politica, nil
}

// analisarDuracao interpreta uma duracao da configuracao.
//
// Vazio devolve zero SEM erro, e o chamador o le como "nao declarado". Texto
// presente e ilegivel, ao contrario, derruba a partida: alguem quis dizer alguma
// coisa e o gateway nao entendeu, e seguir com o padrao esconderia justamente o
// engano que precisa ser visto.
func analisarDuracao(bruto, campo string) (time.Duration, error) {
	bruto = strings.TrimSpace(bruto)
	if bruto == "" {
		return 0, nil
	}

	duracao, err := time.ParseDuration(bruto)
	if err != nil {
		return 0, falha.Envolver(falha.CategoriaEntradaInvalida, operacaoCarregar,
			"admissao: "+campo+" invalido: "+bruto+". Use 2s, 500ms ou 1m30s", err)
	}
	return duracao, nil
}
