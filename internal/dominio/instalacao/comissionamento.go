package instalacao

import (
	"sort"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
)

// EspecieDeDivergencia classifica o que esta errado entre a origem e a configuracao.
//
// Quatro especies, e a distincao entre elas nao e cosmetica: cada uma tem uma acao
// corretiva DIFERENTE em campo, e juntar todas num "divergencia" generico
// obrigaria o tecnico a descobrir sozinho o que fazer.
type EspecieDeDivergencia uint8

const (
	// DivergenciaCanalNaoConfigurado: a origem declara um canal que a instalacao
	// nao mapeou.
	//
	// Acao: configurar o canal. Comum durante comissionamento, e por isso NAO e
	// tratada como erro — o dado continua sendo gravado enquanto a configuracao
	// nao chega.
	DivergenciaCanalNaoConfigurado EspecieDeDivergencia = iota + 1

	// DivergenciaCanalAusente: a instalacao configura um canal que a origem nao
	// declara.
	//
	// Acao: conferir o painel. Esta e a mais grave das quatro, porque significa um
	// ponto de medicao que NUNCA vai receber dado — e a ausencia de dado nao gera
	// evento nenhum, entao sem esta verificacao ela seria invisivel ate alguem
	// notar um grafico vazio semanas depois.
	DivergenciaCanalAusente

	// DivergenciaGrandeza: a origem acredita medir outra coisa.
	//
	// Acao: conferir a fiacao. E o sintoma classico de canal trocado no painel —
	// dois cabos invertidos produzem duas series plausiveis e ambas erradas.
	DivergenciaGrandeza

	// DivergenciaUnidade: mesma grandeza, unidade diferente.
	//
	// Acao: conferir a parametrizacao do transdutor. Nao invalida o dado, mas o
	// coloca numa escala errada — e um erro que passa despercebido porque a serie
	// continua parecendo razoavel.
	DivergenciaUnidade
)

// String devolve o nome estavel da especie, para o relatorio e para metrica.
func (e EspecieDeDivergencia) String() string {
	switch e {
	case DivergenciaCanalNaoConfigurado:
		return "unconfigured_channel"
	case DivergenciaCanalAusente:
		return "missing_channel"
	case DivergenciaGrandeza:
		return "quantity_mismatch"
	case DivergenciaUnidade:
		return "unit_mismatch"
	}
	return "unknown"
}

// AcaoCorretiva devolve o que fazer, em linguagem de quem esta com a mao no painel.
//
// Existe porque a mensagem de erro de um sistema de comissionamento tem um leitor
// especifico: um eletricista ou um tecnico de manutencao industrial, que sabe de
// painel eletrico e nao de modelo de dados. "Divergencia de grandeza no canal 0/2"
// nao diz a ele o que fazer; "confira a fiacao" diz.
func (e EspecieDeDivergencia) AcaoCorretiva() string {
	switch e {
	case DivergenciaCanalNaoConfigurado:
		return "canal chegando sem configuracao: mapeie-o para um ponto de medicao"
	case DivergenciaCanalAusente:
		return "ponto configurado nunca recebeu dado: confira se o sensor esta ligado e o canal correto"
	case DivergenciaGrandeza:
		return "a origem acredita medir outra grandeza: confira a fiacao do painel"
	case DivergenciaUnidade:
		return "unidade diferente da configurada: confira a parametrizacao do transdutor"
	}
	return "verifique a configuracao da instalacao"
}

// Divergencia e um desacordo entre o que a origem declara e o que a instalacao configura.
type Divergencia struct {
	Especie EspecieDeDivergencia
	Canal   ChaveDeCanal

	// Ponto e o ponto de medicao envolvido, quando ha um configurado.
	Ponto string

	// Declarado e o que a ORIGEM afirma. Esperado e o que a INSTALACAO configura.
	//
	// Os dois viajam juntos porque o valor de um relatorio de comissionamento esta
	// justamente no par: "declara temperatura, esperado pressao" resolve o problema;
	// "divergencia de grandeza" manda o tecnico procurar.
	Declarado string
	Esperado  string
}

// CanalDeclarado e o que a origem afirma sobre um de seus canais.
//
// Tipo proprio em vez de receber o descritor do contrato direto: a verificacao de
// comissionamento e regra de dominio e nao deve conhecer a forma do fio. Quem
// traduz e o chamador, que ja tem o conteudo decodificado em maos.
type CanalDeclarado struct {
	Endereco aquisicao.EnderecoDeCanal
	Grandeza Grandeza
	Unidade  string
}

// ConferirDescritor compara o que uma origem declara com o que a instalacao configura.
//
// Esta e a rede de protecao de comissionamento: sem ela, um canal trocado no painel
// produz uma serie perfeitamente plausivel e completamente errada, e o erro so
// aparece quando alguem estranha um numero — se aparecer.
//
// A verificacao roda nos DOIS sentidos de proposito. Conferir apenas o que a origem
// manda deixaria passar o caso mais grave: um ponto configurado que nunca recebe
// dado. Ausencia de dado nao gera evento, entao ela e invisivel sem que alguem
// pergunte ativamente por ela — e e isso que esta funcao faz.
//
// A configuracao e AUTORITATIVA em todos os casos. Divergencia nunca sobrescreve o
// que a instalacao declara; ela apenas denuncia.
func (i *Instalacao) ConferirDescritor(dispositivo identidadededispositivo.IDDoDispositivo,
	declarados []CanalDeclarado) []Divergencia {

	// Sem pre-alocacao, de proposito, contra a sugestao do linter prealloc.
	//
	// Ele assume que o tamanho final se aproxima do numero de itens percorridos. Aqui
	// e o contrario: em instalacao saudavel o resultado e VAZIO, e essa e a esmagadora
	// maioria das chamadas. Pre-alocar para len(declarados) reservaria, a cada
	// consulta do relatorio, espaco que quase nunca e usado.
	//
	// Uma fatia nula nao aloca nada ate o primeiro append, que e exatamente o
	// comportamento desejado quando o caso comum e "nada a relatar".
	var divergencias []Divergencia //nolint:prealloc // o caso comum e resultado vazio
	vistos := make(map[aquisicao.EnderecoDeCanal]struct{}, len(declarados))

	// Sentido 1: o que a origem declara existe e bate com a configuracao?
	for _, declarado := range declarados {
		vistos[declarado.Endereco] = struct{}{}
		canal := ChaveDeCanal{Dispositivo: dispositivo, Endereco: declarado.Endereco}

		configurado, existe := i.Resolver(canal)
		if !existe {
			divergencias = append(divergencias, Divergencia{
				Especie:   DivergenciaCanalNaoConfigurado,
				Canal:     canal,
				Declarado: NomeDaGrandeza(declarado.Grandeza) + " em " + declarado.Unidade,
			})
			continue
		}

		// Grandeza NAO_ESPECIFICADA na declaracao nao e divergencia: a origem
		// simplesmente nao afirmou nada, e nao afirmar e diferente de discordar.
		if declarado.Grandeza != 0 && declarado.Grandeza != configurado.Grandeza {
			divergencias = append(divergencias, Divergencia{
				Especie:   DivergenciaGrandeza,
				Canal:     canal,
				Ponto:     configurado.Ponto.String(),
				Declarado: NomeDaGrandeza(declarado.Grandeza),
				Esperado:  NomeDaGrandeza(configurado.Grandeza),
			})
			continue
		}

		if declarado.Unidade != "" && declarado.Unidade != configurado.Unidade {
			divergencias = append(divergencias, Divergencia{
				Especie:   DivergenciaUnidade,
				Canal:     canal,
				Ponto:     configurado.Ponto.String(),
				Declarado: declarado.Unidade,
				Esperado:  configurado.Unidade,
			})
		}
	}

	// Sentido 2: algum canal configurado para este dispositivo ficou de fora?
	for _, canal := range i.CanaisConfigurados() {
		if canal.Dispositivo != dispositivo {
			continue
		}
		if _, declarado := vistos[canal.Endereco]; declarado {
			continue
		}
		configurado, _ := i.Resolver(canal)
		divergencias = append(divergencias, Divergencia{
			Especie:  DivergenciaCanalAusente,
			Canal:    canal,
			Ponto:    configurado.Ponto.String(),
			Esperado: NomeDaGrandeza(configurado.Grandeza) + " em " + configurado.Unidade,
		})
	}

	// Ordem estavel: o relatorio e comparado entre duas execucoes por quem esta
	// comissionando, e uma listagem que muda de ordem torna essa comparacao inutil.
	sort.SliceStable(divergencias, func(primeiro, segundo int) bool {
		if divergencias[primeiro].Canal.String() != divergencias[segundo].Canal.String() {
			return divergencias[primeiro].Canal.String() < divergencias[segundo].Canal.String()
		}
		return divergencias[primeiro].Especie < divergencias[segundo].Especie
	})
	return divergencias
}

// ConferirVersaoDoCatalogoDeMotivos detecta deriva de vocabulario.
//
// Se a origem exibe rotulos de uma versao antiga do catalogo, os codigos que ela
// envia podem significar OUTRA COISA — e o dado fica errado de forma indetectavel,
// porque o codigo continua sendo um inteiro valido.
//
// Versao zero na origem significa "nao declarada", e nao e divergencia: origem
// sem interface de operador nao tem catalogo para carregar.
func (i *Instalacao) ConferirVersaoDoCatalogoDeMotivos(versaoDaOrigem uint32) bool {
	if versaoDaOrigem == 0 {
		return true
	}
	return versaoDaOrigem == i.versaoDosMotivos
}
