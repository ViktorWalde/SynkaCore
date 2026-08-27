package instalacao

import (
	"sort"
	"strings"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/pontodemedicao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const (
	operacaoNovaInstalacao = "instalacao.NovaInstalacao"
	operacaoResolver       = "instalacao.Resolver"

	tamanhoMaximoDoIDDaInstalacao = 64
)

// ChaveDeCanal identifica uma entrada fisica na instalacao inteira.
//
// Tipo proprio em vez de dois campos soltos pela mesma razao que o contrato aninha
// o endereco: a funcao que resolve recebe UM argumento tipado, e ninguem consegue
// trocar a ordem de dispositivo e endereco numa chamada.
type ChaveDeCanal struct {
	Dispositivo identidadededispositivo.IDDoDispositivo
	Endereco    aquisicao.EnderecoDeCanal
}

// String devolve a forma textual canonica da chave, para log e diagnostico.
func (c ChaveDeCanal) String() string {
	var construtor strings.Builder
	construtor.WriteString(c.Dispositivo.String())
	construtor.WriteByte('@')
	construtor.WriteString(c.Endereco.String())
	return construtor.String()
}

// PontoConfigurado e o que a instalacao declara sobre um canal.
//
// E a autoridade sobre o significado do numero que a origem envia. O descritor que
// a origem manda declara o que ela ACREDITA medir, e serve apenas para detectar
// discordancia — nunca para sobrescrever isto.
type PontoConfigurado struct {
	Ponto    pontodemedicao.IDDoPontoDeMedicao
	Grandeza Grandeza

	// Unidade em notacao UCUM (por exemplo "Cel", "kPa", "L/min", "kg").
	Unidade string

	// Faixa plausivel do instrumento. Nula significa "nao declarada".
	//
	// Serve para MARCAR leitura fora de faixa, nunca para recusa-la: a origem
	// mediu aquilo, e descartar a medicao apagaria justamente o sintoma de um
	// instrumento descalibrado ou de um cabo rompido. O valor entra no modelo de
	// leitura com a anomalia declarada ao lado.
	FaixaMinima *float64
	FaixaMaxima *float64
}

// ForaDeFaixa informa se o valor esta fora da faixa plausivel declarada.
//
// Faixa nao declarada nunca produz anomalia: ausencia de configuracao nao e o
// mesmo que violacao dela, e tratar as duas igual encheria o sistema de alarme
// falso em toda instalacao ainda nao configurada por completo.
func (p PontoConfigurado) ForaDeFaixa(valor float64) bool {
	if p.FaixaMinima != nil && valor < *p.FaixaMinima {
		return true
	}
	return p.FaixaMaxima != nil && valor > *p.FaixaMaxima
}

// Instalacao e a configuracao completa de uma planta.
//
// Imutavel apos a construcao: os campos sao nao exportados e nao ha metodo que
// mute. Recarregar configuracao e construir OUTRA instalacao e trocar a
// referencia, nunca alterar esta sob os pes de quem esta usando.
type Instalacao struct {
	id               string
	pontosPorCanal   map[ChaveDeCanal]PontoConfigurado
	motivos          map[uint32]string
	versaoDosMotivos uint32
}

// ParametrosDeInstalacao e a forma bruta com que a configuracao chega do arquivo.
//
// Como ParametrosDeEnvelope, nao e um modelo paralelo de dominio: nao tem
// comportamento e nao circula pelo sistema. E a assinatura do construtor.
type ParametrosDeInstalacao struct {
	ID               string
	Pontos           map[ChaveDeCanal]PontoConfigurado
	Motivos          map[uint32]string
	VersaoDosMotivos uint32
}

// NovaInstalacao valida e constroi a configuracao.
//
// Este e o UNICO ponto de validacao de configuracao do sistema, e ele roda na
// PARTIDA. Configuracao invalida derruba o gateway com mensagem clara em vez de
// produzir dado errado em silencio meses depois — a mesma regra que a V1.x ja
// aplicava com validacao de settings no startup.
func NovaInstalacao(parametros ParametrosDeInstalacao) (*Instalacao, error) {
	if parametros.ID == "" {
		return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
			"instalacao sem identificador")
	}
	if len(parametros.ID) > tamanhoMaximoDoIDDaInstalacao {
		return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
			"identificador de instalacao excede o comprimento maximo")
	}
	if len(parametros.Pontos) == 0 {
		return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
			"instalacao sem nenhum ponto de medicao configurado")
	}

	// Um ponto de medicao alimentado por dois canais ao mesmo tempo e ambiguidade,
	// nao redundancia: as duas leituras iriam para a mesma serie e o grafico
	// mostraria uma oscilacao que nao existe no equipamento. Se o mesmo ponto tem
	// mesmo duas fontes, isso e um vinculo datado — uma substitui a outra no tempo
	// —, e nao duas entradas simultaneas.
	canaisPorPonto := make(map[string]ChaveDeCanal, len(parametros.Pontos))

	for canal, ponto := range parametros.Pontos {
		if canal.Dispositivo.Vazio() {
			return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
				"canal configurado sem dispositivo")
		}
		if ponto.Ponto.Vazio() {
			return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
				"canal "+canal.String()+" sem ponto de medicao")
		}
		if ponto.Grandeza == 0 {
			return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
				"ponto "+ponto.Ponto.String()+" sem grandeza declarada")
		}
		if strings.TrimSpace(ponto.Unidade) == "" {
			return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
				"ponto "+ponto.Ponto.String()+" sem unidade declarada")
		}
		if ponto.FaixaMinima != nil && ponto.FaixaMaxima != nil &&
			*ponto.FaixaMinima > *ponto.FaixaMaxima {
			return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
				"ponto "+ponto.Ponto.String()+" com faixa invertida")
		}

		nomeDoPonto := ponto.Ponto.String()
		if anterior, repetido := canaisPorPonto[nomeDoPonto]; repetido {
			return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
				"ponto de medicao "+nomeDoPonto+" alimentado por dois canais ao mesmo tempo: "+
					anterior.String()+" e "+canal.String())
		}
		canaisPorPonto[nomeDoPonto] = canal
	}

	// Copia defensiva dos dois mapas: quem construiu os parametros continua com a
	// referencia, e sem a copia poderia alterar a configuracao depois de ela ter
	// sido validada.
	pontos := make(map[ChaveDeCanal]PontoConfigurado, len(parametros.Pontos))
	for canal, ponto := range parametros.Pontos {
		pontos[canal] = ponto
	}
	motivos := make(map[uint32]string, len(parametros.Motivos))
	for codigo, rotulo := range parametros.Motivos {
		if codigo == 0 {
			return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
				"codigo de motivo zero e reservado para parada nao classificada")
		}
		motivos[codigo] = rotulo
	}

	return &Instalacao{
		id:               parametros.ID,
		pontosPorCanal:   pontos,
		motivos:          motivos,
		versaoDosMotivos: parametros.VersaoDosMotivos,
	}, nil
}

// ID devolve o identificador da instalacao.
func (i *Instalacao) ID() string { return i.id }

// VersaoDoCatalogoDeMotivos devolve a versao do vocabulario de paradas.
func (i *Instalacao) VersaoDoCatalogoDeMotivos() uint32 { return i.versaoDosMotivos }

// Resolver devolve o ponto de medicao alimentado por um canal.
//
// O segundo retorno distingue "nao configurado" de "configurado", sem obrigar o
// chamador a interpretar um erro. Canal nao configurado NAO e falha: e uma origem
// enviando algo que ninguem mapeou ainda, o que acontece durante comissionamento e
// e informacao util — o dado continua sendo gravado, e a lacuna de configuracao
// aparece no relatorio de comissionamento em vez de derrubar a ingestao.
func (i *Instalacao) Resolver(canal ChaveDeCanal) (PontoConfigurado, bool) {
	ponto, configurado := i.pontosPorCanal[canal]
	return ponto, configurado
}

// RotuloDoMotivo traduz um codigo de parada para o rotulo da instalacao.
//
// Codigo zero significa parada AINDA NAO CLASSIFICADA, que e informacao legitima e
// nao deve ser confundida com dado faltando: devolve rotulo vazio e "reconhecido",
// porque o zero e um valor previsto do vocabulario, nao uma violacao dele.
func (i *Instalacao) RotuloDoMotivo(codigo uint32) (string, bool) {
	if codigo == 0 {
		return "", true
	}
	rotulo, reconhecido := i.motivos[codigo]
	return rotulo, reconhecido
}

// CanaisConfigurados devolve todos os canais da instalacao, em ordem estavel.
//
// Ordem estavel importa porque isto alimenta o relatorio de comissionamento, que
// um tecnico compara entre duas execucoes. Uma listagem que muda de ordem a cada
// consulta — o que um mapa produz naturalmente — tornaria essa comparacao inutil.
func (i *Instalacao) CanaisConfigurados() []ChaveDeCanal {
	canais := make([]ChaveDeCanal, 0, len(i.pontosPorCanal))
	for canal := range i.pontosPorCanal {
		canais = append(canais, canal)
	}
	sort.Slice(canais, func(primeiro, segundo int) bool {
		return canais[primeiro].String() < canais[segundo].String()
	})
	return canais
}
