package instalacao

import (
	"sort"
	"strings"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/pontodemedicao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const (
	operacaoNovaInstalacao = "instalacao.NovaInstalacao"

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

// PontoConfigurado e o que a instalacao declara sobre um canal, num periodo.
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
	// instrumento descalibrado ou de um cabo rompido.
	FaixaMinima *float64
	FaixaMaxima *float64

	// VigenteDe e VigenteAte delimitam quando este mapeamento vale, como intervalo
	// fechado-aberto [VigenteDe, VigenteAte).
	//
	// Existem porque a serie historica pertence ao PONTO DE MEDICAO, nao a peca de
	// hardware: trocar um sensor queimado nao pode romper a continuidade da serie,
	// e o dado gravado antes da troca precisa continuar sendo interpretado com a
	// configuracao que valia NAQUELE momento.
	//
	// Sem vigencia, corrigir a unidade de um ponto hoje reinterpretaria
	// retroativamente todo o historico — e uma serie que muda de significado ao
	// se editar um arquivo nao e uma serie confiavel.
	//
	// Zero em VigenteDe significa "desde sempre"; zero em VigenteAte, "ainda
	// aberto". Os dois zeros juntos sao o caso comum de uma instalacao que nunca
	// trocou nada, e por isso a vigencia e OPCIONAL na configuracao.
	VigenteDe  time.Time
	VigenteAte time.Time
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

// CobreInstante informa se este mapeamento valia no instante indicado.
func (p PontoConfigurado) CobreInstante(instante time.Time) bool {
	if !p.VigenteDe.IsZero() && instante.Before(p.VigenteDe) {
		return false
	}
	return p.VigenteAte.IsZero() || instante.Before(p.VigenteAte)
}

// sobrepoe informa se duas vigencias se cruzam no tempo.
//
// Sobreposicao e ambiguidade, e por isso e recusada na construcao: duas
// configuracoes valendo ao mesmo tempo para o mesmo canal fariam a interpretacao
// do dado depender de qual delas o codigo encontrasse primeiro.
func (p PontoConfigurado) sobrepoe(outro PontoConfigurado) bool {
	comecaDepoisDoFimDoOutro := !outro.VigenteAte.IsZero() &&
		!p.VigenteDe.Before(outro.VigenteAte)
	terminaAntesDoInicioDoOutro := !p.VigenteAte.IsZero() &&
		!outro.VigenteDe.Before(p.VigenteAte)
	return !comecaDepoisDoFimDoOutro && !terminaAntesDoInicioDoOutro
}

// Instalacao e a configuracao completa de uma planta, ao longo do tempo.
//
// Imutavel apos a construcao: os campos sao nao exportados e nao ha metodo que
// mute. Recarregar configuracao e construir OUTRA instalacao e trocar a
// referencia, nunca alterar esta sob os pes de quem esta usando.
type Instalacao struct {
	id string

	// Uma LISTA por canal, e nao um valor: o mesmo canal pode ter alimentado
	// pontos diferentes em periodos diferentes.
	pontosPorCanal   map[ChaveDeCanal][]PontoConfigurado
	motivos          map[uint32]string
	versaoDosMotivos uint32

	// admissao e quanto cada classe de dado tolera esperar na porta do gateway.
	//
	// Fica aqui, e nao numa bandeira de linha de comando, porque e uma afirmacao
	// sobre a PLANTA: quanto uma amostra desta instalacao pode envelhecer antes de
	// valer menos que a que vem atras dela. Bandeira some na proxima unidade
	// systemd que alguem reescrever; este arquivo e versionado e revisavel.
	admissao Admissao
}

// ParametrosDeInstalacao e a forma bruta com que a configuracao chega do arquivo.
//
// Como ParametrosDeEnvelope, nao e um modelo paralelo de dominio: nao tem
// comportamento e nao circula pelo sistema. E a assinatura do construtor.
type ParametrosDeInstalacao struct {
	ID string

	// Mapeamentos e a lista COMPLETA, incluindo vigencias ja encerradas. A ordem
	// nao importa; a construcao ordena por vigencia.
	Mapeamentos      map[ChaveDeCanal][]PontoConfigurado
	Motivos          map[uint32]string
	VersaoDosMotivos uint32

	// Admissao e opcional. Nula, valem os padroes dimensionados pela medicao da
	// V2.3 — que e o caso de toda instalacao que nunca precisou pensar no assunto,
	// e portanto o caso comum.
	Admissao *Admissao
}

// NovaInstalacao valida e constroi a configuracao.
//
// Este e o UNICO ponto de validacao de configuracao do sistema, e ele roda na
// PARTIDA. Configuracao invalida derruba o gateway com mensagem clara em vez de
// produzir dado errado em silencio meses depois.
func NovaInstalacao(parametros ParametrosDeInstalacao) (*Instalacao, error) {
	if parametros.ID == "" {
		return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
			"instalacao sem identificador")
	}
	if len(parametros.ID) > tamanhoMaximoDoIDDaInstalacao {
		return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
			"identificador de instalacao excede o comprimento maximo")
	}
	if len(parametros.Mapeamentos) == 0 {
		return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
			"instalacao sem nenhum ponto de medicao configurado")
	}

	pontos := make(map[ChaveDeCanal][]PontoConfigurado, len(parametros.Mapeamentos))
	// vigenciasPorPonto acumula todas as vigencias de cada ponto, para detectar
	// dois canais alimentando o mesmo ponto AO MESMO TEMPO.
	vigenciasPorPonto := make(map[string][]vigenciaDeCanal)

	for canal, mapeamentos := range parametros.Mapeamentos {
		if canal.Dispositivo.Vazio() {
			return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
				"canal configurado sem dispositivo")
		}
		if len(mapeamentos) == 0 {
			return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
				"canal "+canal.String()+" sem nenhum mapeamento")
		}

		validados := make([]PontoConfigurado, 0, len(mapeamentos))
		for _, mapeamento := range mapeamentos {
			if err := validarMapeamento(canal, mapeamento); err != nil {
				return nil, err
			}

			// Duas vigencias do MESMO canal nao podem se cruzar: o gateway nao teria
			// como decidir qual configuracao aplicar a uma leitura daquele periodo.
			for _, ja := range validados {
				if mapeamento.sobrepoe(ja) {
					return nil, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
						"canal "+canal.String()+" tem duas vigencias sobrepostas: "+
							ja.Ponto.String()+" e "+mapeamento.Ponto.String())
				}
			}
			validados = append(validados, mapeamento)

			nome := mapeamento.Ponto.String()
			vigenciasPorPonto[nome] = append(vigenciasPorPonto[nome],
				vigenciaDeCanal{canal: canal, ponto: mapeamento})
		}

		// Ordenadas por inicio de vigencia: a resolucao percorre e para na primeira
		// que cobre o instante, e a ordem estavel torna o resultado reproduzivel.
		sort.SliceStable(validados, func(primeiro, segundo int) bool {
			return validados[primeiro].VigenteDe.Before(validados[segundo].VigenteDe)
		})
		pontos[canal] = validados
	}

	if err := recusarPontosAmbiguos(vigenciasPorPonto); err != nil {
		return nil, err
	}

	admissao, err := resolverAdmissao(parametros.Admissao)
	if err != nil {
		return nil, err
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
		admissao:         admissao,
	}, nil
}

// resolverAdmissao devolve a politica declarada, ja validada, ou o padrao.
//
// Funcao separada, e nao um bloco dentro de NovaInstalacao, porque o linter de
// complexidade cognitiva cobrou — e ele estava certo. NovaInstalacao ja carrega
// tres regras que se cruzam (vigencia sobreposta no canal, ponto ambiguo entre
// canais, codigo de motivo reservado); acrescentar uma quarta decisao no mesmo
// corpo e como uma funcao de validacao vira aquela que ninguem consegue mais ler
// inteira antes de mexer.
func resolverAdmissao(declarada *Admissao) (Admissao, error) {
	if declarada == nil {
		return AdmissaoPadrao(), nil
	}
	return NovaAdmissao(*declarada)
}

// vigenciaDeCanal liga um mapeamento ao canal que o declarou, para a checagem de
// ambiguidade de ponto.
type vigenciaDeCanal struct {
	canal ChaveDeCanal
	ponto PontoConfigurado
}

// validarMapeamento confere um mapeamento isolado.
func validarMapeamento(canal ChaveDeCanal, mapeamento PontoConfigurado) error {
	if mapeamento.Ponto.Vazio() {
		return falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
			"canal "+canal.String()+" sem ponto de medicao")
	}
	if mapeamento.Grandeza == 0 {
		return falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
			"ponto "+mapeamento.Ponto.String()+" sem grandeza declarada")
	}
	if strings.TrimSpace(mapeamento.Unidade) == "" {
		return falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
			"ponto "+mapeamento.Ponto.String()+" sem unidade declarada")
	}
	if mapeamento.FaixaMinima != nil && mapeamento.FaixaMaxima != nil &&
		*mapeamento.FaixaMinima > *mapeamento.FaixaMaxima {
		return falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
			"ponto "+mapeamento.Ponto.String()+" com faixa invertida")
	}
	if !mapeamento.VigenteDe.IsZero() && !mapeamento.VigenteAte.IsZero() &&
		!mapeamento.VigenteDe.Before(mapeamento.VigenteAte) {
		return falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
			"ponto "+mapeamento.Ponto.String()+" com vigencia invertida ou vazia")
	}
	return nil
}

// recusarPontosAmbiguos impede que dois canais alimentem o mesmo ponto ao mesmo tempo.
//
// Nao e redundancia, e ambiguidade: as duas leituras iriam para a mesma serie e o
// grafico mostraria uma oscilacao que nao existe no equipamento. Um ponto pode ter
// duas fontes ao longo do TEMPO — e isso e a troca de sensor, expressa por
// vigencias que nao se cruzam.
func recusarPontosAmbiguos(vigenciasPorPonto map[string][]vigenciaDeCanal) error {
	// Ordem estavel dos nomes: sem ela, a mesma configuracao invalida acusaria um
	// ponto diferente a cada execucao, e a mensagem de erro deixaria de ser
	// reproduzivel para quem esta tentando corrigi-la.
	nomes := make([]string, 0, len(vigenciasPorPonto))
	for nome := range vigenciasPorPonto {
		nomes = append(nomes, nome)
	}
	sort.Strings(nomes)

	for _, nome := range nomes {
		vigencias := vigenciasPorPonto[nome]
		for primeiro := range vigencias {
			for segundo := primeiro + 1; segundo < len(vigencias); segundo++ {
				if vigencias[primeiro].canal == vigencias[segundo].canal {
					continue
				}
				if vigencias[primeiro].ponto.sobrepoe(vigencias[segundo].ponto) {
					return falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaInstalacao,
						"ponto de medicao "+nome+" alimentado por dois canais ao mesmo tempo: "+
							vigencias[primeiro].canal.String()+" e "+vigencias[segundo].canal.String()+
							". Para registrar troca de sensor, declare vigencias que nao se cruzam")
				}
			}
		}
	}
	return nil
}

// ID devolve o identificador da instalacao.
func (i *Instalacao) ID() string { return i.id }

// VersaoDoCatalogoDeMotivos devolve a versao do vocabulario de paradas.
func (i *Instalacao) VersaoDoCatalogoDeMotivos() uint32 { return i.versaoDosMotivos }

// Resolver devolve o ponto de medicao que um canal alimentava NO INSTANTE indicado.
//
// O instante importa e nao e detalhe: uma leitura gravada antes de uma troca de
// sensor precisa continuar sendo interpretada com a configuracao que valia naquele
// momento. Resolver sempre pela configuracao atual reinterpretaria o historico
// inteiro a cada edicao do arquivo.
//
// O segundo retorno distingue "nao configurado" de "configurado", sem obrigar o
// chamador a interpretar um erro. Canal nao configurado NAO e falha: e uma origem
// enviando algo que ninguem mapeou ainda, o que acontece durante comissionamento —
// o dado continua sendo gravado, e a lacuna aparece no relatorio.
func (i *Instalacao) Resolver(canal ChaveDeCanal, instante time.Time) (PontoConfigurado, bool) {
	for _, mapeamento := range i.pontosPorCanal[canal] {
		if mapeamento.CobreInstante(instante) {
			return mapeamento, true
		}
	}
	return PontoConfigurado{}, false
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

// VinculosDoPonto devolve o historico de quais dispositivos alimentaram um ponto.
//
// Esta e a resposta a pergunta que a vigencia existe para permitir: "qual
// dispositivo alimentava este ponto em tal instante?" — respondivel anos depois,
// sem depender de historico de git nem da memoria de quem trocou o sensor.
//
// Devolve pontodemedicao.Vinculo, e nao um tipo proprio, porque e exatamente o que
// aquele tipo modela: a ligacao temporal entre peca de hardware e posicao na
// planta. Um tipo novo aqui seria um segundo modelo do mesmo conceito.
func (i *Instalacao) VinculosDoPonto(ponto pontodemedicao.IDDoPontoDeMedicao) []pontodemedicao.Vinculo {
	var vinculos []pontodemedicao.Vinculo //nolint:prealloc // um ponto tem poucos vinculos, quase sempre um

	for _, canal := range i.CanaisConfigurados() {
		for _, mapeamento := range i.pontosPorCanal[canal] {
			if mapeamento.Ponto != ponto {
				continue
			}

			// NovoVinculo exige instante inicial. Vigencia "desde sempre" nao tem um,
			// e inventar uma data seria afirmar algo que a configuracao nao diz —
			// entao ela e representada pelo unico instante que nao afirma nada sobre
			// quando comecou: o inicio do tempo do calendario Unix.
			vigenteDe := mapeamento.VigenteDe
			if vigenteDe.IsZero() {
				vigenteDe = time.Unix(0, 0).UTC()
			}

			vinculo, err := pontodemedicao.NovoVinculo(ponto, canal.Dispositivo, vigenteDe)
			if err != nil {
				continue
			}
			if !mapeamento.VigenteAte.IsZero() {
				encerrado, err := vinculo.EncerradoEm(mapeamento.VigenteAte)
				if err == nil {
					vinculo = encerrado
				}
			}
			vinculos = append(vinculos, vinculo)
		}
	}

	sort.SliceStable(vinculos, func(primeiro, segundo int) bool {
		return vinculos[primeiro].VigenteDe().Before(vinculos[segundo].VigenteDe())
	})
	return vinculos
}
