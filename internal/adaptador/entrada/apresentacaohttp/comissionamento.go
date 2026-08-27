package apresentacaohttp

import (
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/instalacao"
)

// declaracaoDaOrigem e o ultimo descritor recebido de um dispositivo.
type declaracaoDaOrigem struct {
	canais                    []instalacao.CanalDeclarado
	versaoDoCatalogoDeMotivos uint32
	versaoDoFirmware          string
	recebidaEm                time.Time
}

// registroDeDeclaracoes guarda o que cada origem declarou por ultimo.
//
// Protegido por mutex porque o caminho de aquisicao escreve enquanto a
// apresentacao le, e os dois rodam em goroutines diferentes.
type registroDeDeclaracoes struct {
	mutex          sync.RWMutex
	porDispositivo map[string]declaracaoDaOrigem
}

func novoRegistroDeDeclaracoes() *registroDeDeclaracoes {
	return &registroDeDeclaracoes{porDispositivo: make(map[string]declaracaoDaOrigem)}
}

// RegistrarDescritor guarda o que uma origem declarou sobre si.
//
// Chamado pela projecao ao encontrar um descritor no diario, e nao pela ingestao:
// assim o relatorio se reconstroi sozinho ao reprocessar o diario, e o caminho de
// aquisicao — que nunca pode parar — nao ganha nenhum trabalho a mais.
func (a *Apresentacao) RegistrarDescritor(dispositivo string, descritor aquisicao.DescritorDaOrigem,
	recebidaEm time.Time) {

	canais := make([]instalacao.CanalDeclarado, 0, len(descritor.Canais))
	for _, canal := range descritor.Canais {
		canais = append(canais, instalacao.CanalDeclarado{
			Endereco: canal.Endereco,
			Grandeza: instalacao.Grandeza(canal.Grandeza),
			Unidade:  canal.Unidade,
		})
	}

	a.declaracoes.mutex.Lock()
	defer a.declaracoes.mutex.Unlock()
	a.declaracoes.porDispositivo[dispositivo] = declaracaoDaOrigem{
		canais:                    canais,
		versaoDoCatalogoDeMotivos: descritor.VersaoDoCatalogoDeMotivos,
		versaoDoFirmware:          descritor.VersaoDoFirmware,
		recebidaEm:                recebidaEm,
	}
}

// divergenciaRelatada e uma linha do relatorio de comissionamento.
type divergenciaRelatada struct {
	Kind             string `json:"kind"`
	DeviceID         string `json:"device_id"`
	Channel          string `json:"channel"`
	MeasurementPoint string `json:"measurement_point,omitempty"`
	Declared         string `json:"declared,omitempty"`
	Expected         string `json:"expected,omitempty"`

	// Action carrega a orientacao em linguagem de quem esta com a mao no painel.
	//
	// O leitor deste relatorio e um eletricista ou tecnico de manutencao, que sabe
	// de painel eletrico e nao de modelo de dados. "quantity_mismatch" nao diz a
	// ele o que fazer; "confira a fiacao do painel" diz.
	Action string `json:"action"`
}

// origemRelatada resume o estado de uma origem no relatorio.
type origemRelatada struct {
	DeviceID           string `json:"device_id"`
	FirmwareVersion    string `json:"firmware_version,omitempty"`
	DeclaredChannels   int    `json:"declared_channels"`
	LastDescriptorAt   string `json:"last_descriptor_at"`
	ReasonCatalogDrift bool   `json:"reason_catalog_drift"`
}

// relatorioDeComissionamento e o corpo da resposta.
type relatorioDeComissionamento struct {
	Installation string `json:"installation"`

	// Configured informa se ha configuracao de instalacao carregada.
	//
	// Sem este campo, um gateway sem configuracao devolveria zero divergencias — e
	// "nenhuma divergencia" e exatamente a resposta que faria o tecnico concluir
	// que esta tudo certo. Dizer que nada foi conferido e a resposta honesta.
	Configured bool `json:"configured"`

	// SourcesSeen informa quantas origens ja enviaram descritor NESTA execucao.
	//
	// Zero, com configuracao carregada, significa "ainda nao ha o que conferir" e
	// nao "esta tudo certo". A distincao esta no campo, e nao subentendida.
	SourcesSeen int `json:"sources_seen"`

	Sources     []origemRelatada      `json:"sources"`
	Divergences []divergenciaRelatada `json:"divergences"`
	CheckedAt   string                `json:"checked_at"`
}

// responderComissionamento compara o que cada origem declara com a configuracao.
//
// Devolve 200 mesmo com divergencias: o relatorio foi produzido com sucesso, e
// divergencia e o CONTEUDO dele, nao uma falha em obte-lo. Devolver erro faria um
// monitoramento tratar comissionamento em andamento como indisponibilidade.
func (a *Apresentacao) responderComissionamento(escritor http.ResponseWriter, _ *http.Request) {
	relatorio := relatorioDeComissionamento{
		Configured:  a.configuracao != nil,
		CheckedAt:   a.relogio.Agora().Format(time.RFC3339),
		Sources:     []origemRelatada{},
		Divergences: []divergenciaRelatada{},
	}
	if a.configuracao != nil {
		relatorio.Installation = a.configuracao.ID()
	}

	// A conferencia usa o instante da CONSULTA, e nao o da declaracao: o tecnico
	// quer saber se a fiacao esta certa AGORA. Um canal cuja vigencia terminou
	// ontem nao deve aparecer como divergencia hoje.
	agora := a.relogio.Agora()

	a.declaracoes.mutex.RLock()
	declaracoes := make(map[string]declaracaoDaOrigem, len(a.declaracoes.porDispositivo))
	for nome, declaracao := range a.declaracoes.porDispositivo {
		declaracoes[nome] = declaracao
	}
	a.declaracoes.mutex.RUnlock()

	relatorio.SourcesSeen = len(declaracoes)

	// Ordem estavel por dispositivo: o tecnico compara o relatorio antes e depois
	// de mexer na fiacao, e uma listagem que muda de ordem torna isso inutil.
	nomes := make([]string, 0, len(declaracoes))
	for nome := range declaracoes {
		nomes = append(nomes, nome)
	}
	sort.Strings(nomes)

	for _, nome := range nomes {
		declaracao := declaracoes[nome]

		origem := origemRelatada{
			DeviceID:         nome,
			FirmwareVersion:  declaracao.versaoDoFirmware,
			DeclaredChannels: len(declaracao.canais),
			LastDescriptorAt: declaracao.recebidaEm.Format(time.RFC3339),
		}

		if a.configuracao == nil {
			relatorio.Sources = append(relatorio.Sources, origem)
			continue
		}

		origem.ReasonCatalogDrift = !a.configuracao.ConferirVersaoDoCatalogoDeMotivos(
			declaracao.versaoDoCatalogoDeMotivos)
		relatorio.Sources = append(relatorio.Sources, origem)

		dispositivo, err := identidadededispositivo.AnalisarIDDoDispositivo(nome)
		if err != nil {
			continue
		}
		for _, divergencia := range a.configuracao.ConferirDescritor(dispositivo, declaracao.canais, agora) {
			relatorio.Divergences = append(relatorio.Divergences, divergenciaRelatada{
				Kind:             divergencia.Especie.String(),
				DeviceID:         nome,
				Channel:          divergencia.Canal.Endereco.String(),
				MeasurementPoint: divergencia.Ponto,
				Declared:         divergencia.Declarado,
				Expected:         divergencia.Esperado,
				Action:           divergencia.Especie.AcaoCorretiva(),
			})
		}
	}

	a.responderJSON(escritor, http.StatusOK, relatorio)
}
