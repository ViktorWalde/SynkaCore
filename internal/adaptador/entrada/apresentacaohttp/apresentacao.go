// Package apresentacaohttp expoe consulta e saude para o lado de ESCRITORIO.
//
// Este package existe separado de ingressohttp por uma razao de topologia, nao de
// organizacao de codigo. O gateway fica entre duas redes: a de chao de fabrica,
// onde so ha origens que nos mesmos provisionamos, e a de escritorio, do cliente,
// onde ha estacoes que nao administramos, possivelmente desatualizadas,
// possivelmente infectadas, na rede onde as pessoas leem e-mail.
//
// O instinto e endurecer o lado OT. Mas o lado OT e o mais controlado que existe —
// e o lado TI e onde esta a superficie de ataque real. Dai as regras que este
// package obedece:
//
//  1. O gateway NAO ROTEIA. Ele e endpoint nas duas redes, nunca ponte de trafego.
//  2. Nada daqui alcanca o caminho de aquisicao. Este package le; nunca grava.
//  3. Os dois servidores escutam em interfaces distintas, e e por isso que sao
//     dois multiplexadores e nao dois caminhos no mesmo.
//
// Um unico ServeMux com /ingestao e /saude juntos pareceria mais simples e
// destruiria a separacao: bastaria alguem publicar a porta errada para o caminho
// de aquisicao ficar exposto ao escritorio.
package apresentacaohttp

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/saida/diariosqlite"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/estadooperacional"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/instalacao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/contrapressao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/relogio"
)

const (
	// CaminhoDeSaude responde se o gateway esta operacional.
	CaminhoDeSaude = "/saude"

	// CaminhoDeLeituras devolve os registros mais recentes do diario.
	CaminhoDeLeituras = "/leituras"

	// CaminhoDoContrato lista os tipos de conteudo que este gateway reconhece.
	CaminhoDoContrato = "/contrato"

	// CaminhoDeComissionamento relata desacordos entre o que as origens declaram e
	// o que a instalacao configura.
	//
	// Existe para o tecnico em campo: ele confere o relatorio, mexe na fiacao,
	// confere de novo. Sem endpoint, essa conferencia exigiria ler log do gateway
	// — que ele nao tem acesso, e que nao esta organizado para isso.
	CaminhoDeComissionamento = "/comissionamento"

	// leiturasPorPaginaPadrao herda o comportamento do endpoint equivalente da V1.x.
	leiturasPorPaginaPadrao = 100

	// leiturasPorPaginaMaximo limita o que uma consulta pode arrastar do disco.
	leiturasPorPaginaMaximo = 1000
)

// Apresentacao e o adaptador HTTP do lado de escritorio.
type Apresentacao struct {
	diario     *diariosqlite.Diario
	catalogo   *aquisicao.CatalogoDeConteudo
	rastreador *estadooperacional.Rastreador
	relogio    relogio.Relogio
	registro   *slog.Logger

	// projecaoLigada informa se este gateway foi configurado para projetar.
	//
	// Existe para que a saude distinga "a projecao caiu" de "nao ha projecao
	// configurada". Sao situacoes com respostas opostas — a primeira pede
	// investigacao, a segunda e a configuracao pedida —, e reporta-las com a mesma
	// palavra faria o monitoramento alarmar sobre uma escolha deliberada.
	projecaoLigada bool

	// configuracao e a instalacao, ou nil quando nenhuma foi carregada.
	configuracao *instalacao.Instalacao

	// portaria e a admissao do caminho de aquisicao, observada apenas para leitura.
	//
	// A apresentacao NAO admite nem recusa nada — ela e o lado de escritorio, e a
	// regra de que nada daqui alcanca o caminho de aquisicao continua valendo. O que
	// ela faz e RELATAR: sem isto, a saturacao existiria, atuaria e nao apareceria em
	// lugar nenhum, que e o defeito que a V2.4 comeca corrigindo.
	portaria *contrapressao.Portaria

	// declaracoes guarda o ultimo descritor recebido de cada dispositivo.
	//
	// Em memoria, e nao no diario, de proposito: e um instantaneo do que as origens
	// estao dizendo AGORA, e nao um fato historico. O descritor bruto continua no
	// diario e permite reconstruir isto a qualquer momento; guardar de novo em
	// disco seria uma segunda copia do mesmo dado, que divergiria.
	//
	// A consequencia aceita: apos reinicio do gateway, o relatorio fica vazio ate
	// cada origem reenviar seu descritor. Isso e dito na propria resposta, em vez
	// de deixar um relatorio vazio parecer "nenhuma divergencia".
	declaracoes *registroDeDeclaracoes
}

// ComProjecaoLigada declara que este gateway projeta para um banco de consulta.
func (a *Apresentacao) ComProjecaoLigada(ligada bool) *Apresentacao {
	a.projecaoLigada = ligada
	return a
}

// NovaApresentacao constroi o adaptador.
func NovaApresentacao(diario *diariosqlite.Diario, catalogo *aquisicao.CatalogoDeConteudo,
	rastreador *estadooperacional.Rastreador, r relogio.Relogio, registro *slog.Logger) *Apresentacao {
	return &Apresentacao{
		diario: diario, catalogo: catalogo, rastreador: rastreador, relogio: r, registro: registro,
		declaracoes: novoRegistroDeDeclaracoes(),
	}
}

// ComContrapressao liga o relato de admissao no health check.
//
// Nula, o /saude reporta "unknown" em vez de "accepting". A escolha e deliberada:
// um campo ausente pareceria ausencia de saturacao, e "unknown" denuncia a propria
// composicao incompleta em vez de afirmar uma saude que ninguem verificou.
func (a *Apresentacao) ComContrapressao(portaria *contrapressao.Portaria) *Apresentacao {
	a.portaria = portaria
	return a
}

// ComInstalacao liga o relatorio de comissionamento.
func (a *Apresentacao) ComInstalacao(configuracao *instalacao.Instalacao) *Apresentacao {
	a.configuracao = configuracao
	return a
}

// Rotas devolve o multiplexador do lado de escritorio.
//
// Somente GET. Nao ha caminho de escrita aqui, e a ausencia e deliberada: o
// SynkaCore observa e nunca comanda equipamento industrial. Um atacante
// bem-sucedido corrompe dado — nao danifica equipamento nem fere pessoas.
func (a *Apresentacao) Rotas() *http.ServeMux {
	rotas := http.NewServeMux()
	rotas.HandleFunc("GET "+CaminhoDeSaude, a.responderSaude)
	rotas.HandleFunc("GET "+CaminhoDeLeituras, a.responderLeituras)
	rotas.HandleFunc("GET "+CaminhoDoContrato, a.responderContrato)
	rotas.HandleFunc("GET "+CaminhoDeComissionamento, a.responderComissionamento)
	rotas.HandleFunc("GET "+CaminhoDoEsboco, a.responderEsboco)
	return rotas
}

// respostaDeSaude e o corpo do health check.
//
// Campos em ingles porque isto e consumido por sistema de monitoramento, nao por
// pessoa lendo codigo. Mesma regra de falha.Categoria.String.
type respostaDeSaude struct {
	Journal         string `json:"journal"`
	Projection      string `json:"projection"`
	ProjectionSince string `json:"projection_since"`

	// Ingestion diz se o gateway esta aceitando remessa de amostra AGORA.
	//
	// Terceira linha independente, pela mesma razao que separou journal de
	// projection: ela tem um destinatario e uma resposta operacional diferentes. As
	// tres respondem coisas distintas, e junta-las produziria um "healthy" que
	// esconde justamente o que decide se alguem age.
	//
	//	journal    — o sistema esta perdendo a capacidade de ACEITAR dado
	//	projection — o dado esta salvo; os dashboards estao atrasados
	//	ingestion  — o gateway esta cheio; as origens estao bufferizando
	Ingestion string `json:"ingestion"`

	// IngestionQueue e quantas remessas aguardam admissao.
	IngestionQueue int `json:"ingestion_queue"`

	// IngestionWaitMs e a espera estimada que o gateway devolve no Retry-After.
	IngestionWaitMs int64 `json:"ingestion_wait_ms"`

	// IngestionShedSamples e IngestionShedEvents contam as recusas SEPARADAS, e a
	// separacao e a informacao.
	//
	// Amostra recusada e a politica funcionando: o gateway esta sacrificando o que
	// tem substituto para proteger o que nao tem. Evento discreto recusado e o teto
	// real sendo ultrapassado — o gateway ficou cheio a ponto de dizer nao ao que
	// nenhuma amostra seguinte repoe. Somadas num unico contador, a segunda
	// desapareceria dentro da primeira, que sobe todo dia sem significar nada.
	IngestionShedSamples uint64 `json:"ingestion_shed_samples"`
	IngestionShedEvents  uint64 `json:"ingestion_shed_events"`

	CheckedAt string `json:"checked_at"`
}

// responderSaude verifica o diario DE VERDADE antes de responder.
//
// A regra que a V1.x ja tinha e que continua valendo: relatar saudavel quando o
// sistema nao esta e ativamente enganoso para quem esta de plantao. A verificacao
// executa consulta real, e nao um ping — uma conexao aberta sobre arquivo
// corrompido responde ao ping e falha na leitura.
//
// A distincao entre as duas linhas da resposta e o que mudou da V1.x para ca:
//
//	journal    — se ISTO falha, o sistema esta perdendo a capacidade de aceitar dado
//	projection — se ISTO falha, o dado esta salvo e os dashboards estao atrasados
//
// So a primeira e motivo para acordar alguem. Juntar as duas num unico "healthy"
// faria o operador tratar uma queda do TimescaleDB como emergencia de aquisicao.
func (a *Apresentacao) responderSaude(escritor http.ResponseWriter, requisicao *http.Request) {
	estado, desde := a.rastreador.Atual()

	projecao := estado.String()
	if !a.projecaoLigada {
		projecao = "disabled"
	}

	resposta := respostaDeSaude{
		Journal:         "available",
		Projection:      projecao,
		ProjectionSince: desde.Format(time.RFC3339),
		Ingestion:       "unknown",
		CheckedAt:       a.relogio.Agora().Format(time.RFC3339),
	}
	a.relatarAdmissao(&resposta)

	status := http.StatusOK
	if err := a.diario.Verificar(requisicao.Context()); err != nil {
		resposta.Journal = "unavailable"
		status = http.StatusServiceUnavailable
		a.registro.Error("o diario nao respondeu a verificacao de saude",
			slog.String("erro", err.Error()))
	}

	a.responderJSON(escritor, status, resposta)
}

// relatarAdmissao preenche as linhas de contrapressao do health check.
//
// SATURACAO NAO ALTERA O STATUS HTTP, e este e o ponto da funcao. Ela devolve 200
// mesmo com o gateway recusando remessa, porque contrapressao e o sistema
// FUNCIONANDO COMO PROJETADO: as origens bufferizam, retransmitem e nada se perde.
//
// Devolver 503 aqui faria um balanceador tirar do ar o gateway que esta apenas
// cheio, e o efeito seria empurrar a carga inteira para os outros — transformando
// uma saturacao parcial em queda total, que e exatamente o modo de falha que o
// jitter do recuo existe para evitar do outro lado.
//
// O que muda o status continua sendo uma coisa so: o diario nao responder.
func (a *Apresentacao) relatarAdmissao(resposta *respostaDeSaude) {
	if a.portaria == nil {
		return
	}

	estado := a.portaria.Estado()

	resposta.Ingestion = "shedding"
	if estado.Admitindo {
		resposta.Ingestion = "accepting"
	}
	resposta.IngestionQueue = estado.Aguardando
	resposta.IngestionWaitMs = estado.EsperaEstimada.Milliseconds()
	resposta.IngestionShedSamples = estado.RecusadasComuns
	resposta.IngestionShedEvents = estado.RecusadasReservadas
}

// leituraProjetada e um registro do diario na forma em que a consulta o devolve.
type leituraProjetada struct {
	SequenceNumber uint64         `json:"sequence_number"`
	DeviceID       string         `json:"device_id"`
	BootSessionID  string         `json:"boot_session_id"`
	ContentType    string         `json:"content_type"`
	DataClass      string         `json:"data_class"`
	UptimeMs       int64          `json:"uptime_ms"`
	ObservedAt     string         `json:"observed_at"`
	Fields         map[string]any `json:"fields,omitempty"`
	DecodeError    string         `json:"decode_error,omitempty"`
}

// responderLeituras devolve os registros mais recentes do diario, ja decodificados.
//
// E o sucessor direto do GET /readings da V1.x, e serve ao mesmo proposito:
// olhar o sistema funcionando sem abrir um cliente de banco. O modelo de leitura
// ANALITICO — series longas, agregacao, dashboards — e o TimescaleDB alimentado
// pela projecao; este endpoint e diagnostico, e o limite de pagina existe para
// que ele nao seja usado como se fosse o outro.
func (a *Apresentacao) responderLeituras(escritor http.ResponseWriter, requisicao *http.Request) {
	limite := a.lerLimite(requisicao)

	registros, err := a.diario.LerAPartirDe(requisicao.Context(), 0, limite)
	if err != nil {
		a.registro.Error("falha ao consultar o diario", slog.String("erro", err.Error()))
		a.responderJSON(escritor, http.StatusServiceUnavailable,
			map[string]string{"error": "journal_unavailable"})
		return
	}

	leituras := make([]leituraProjetada, 0, len(registros))
	for _, registro := range registros {
		leituras = append(leituras, a.decodificarRegistro(registro))
	}
	a.responderJSON(escritor, http.StatusOK, leituras)
}

// decodificarRegistro interpreta o conteudo bruto de um registro para exibicao.
//
// Uma falha de decodificacao NAO derruba a consulta inteira nem some com o
// registro: ela vira um campo na propria linha. Um registro que o gateway nao
// consegue mais interpretar — porque veio de firmware mais novo, ou porque ha um
// defeito nosso — e informacao valiosa, e escondê-lo produziria exatamente o
// buraco silencioso que este projeto trata como inaceitavel.
func (a *Apresentacao) decodificarRegistro(registro diariosqlite.RegistroDoDiario) leituraProjetada {
	leitura := leituraProjetada{
		SequenceNumber: registro.NumeroDeSequencia,
		DeviceID:       registro.IDDoDispositivo,
		BootSessionID:  registro.IDDaSessaoDeBoot,
		ContentType:    registro.TipoDeConteudo,
		DataClass:      registro.ClasseDeDado,
		UptimeMs:       registro.TempoLigado.Milliseconds(),
		ObservedAt:     registro.InstanteObservado.Format(time.RFC3339Nano),
	}

	definicao, err := a.catalogo.Buscar(aquisicao.TipoDeConteudo(registro.TipoDeConteudo))
	if err != nil {
		leitura.DecodeError = "tipo de conteudo nao reconhecido por este gateway"
		return leitura
	}

	conteudo, err := definicao.Decodificar(registro.ConteudoBruto)
	if err != nil {
		leitura.DecodeError = err.Error()
		return leitura
	}

	campos := conteudo.CamposProjetados()
	leitura.Fields = make(map[string]any, len(campos))
	for _, campo := range campos {
		switch valor := campo.Valor.(type) {
		case aquisicao.ValorNumerico:
			leitura.Fields[campo.Nome] = float64(valor)
		case aquisicao.ValorTexto:
			leitura.Fields[campo.Nome] = string(valor)
		case aquisicao.ValorLogico:
			leitura.Fields[campo.Nome] = bool(valor)
		}
	}
	return leitura
}

// tipoDoContrato descreve um tipo de conteudo reconhecido.
type tipoDoContrato struct {
	ContentType string `json:"content_type"`
	Description string `json:"description"`
}

// responderContrato lista o que este gateway sabe interpretar.
//
// Serve ao comissionamento: um tecnico em campo consegue verificar se o gateway
// reconhece o que a origem envia sem precisar de acesso ao codigo, e sem que
// alguem precise ler um log para descobrir por que uma remessa foi recusada.
func (a *Apresentacao) responderContrato(escritor http.ResponseWriter, _ *http.Request) {
	tipos := a.catalogo.Tipos()
	resposta := make([]tipoDoContrato, 0, len(tipos))

	for _, tipo := range tipos {
		definicao, err := a.catalogo.Buscar(tipo)
		if err != nil {
			continue
		}
		resposta = append(resposta, tipoDoContrato{
			ContentType: string(definicao.Tipo),
			Description: definicao.Descricao,
		})
	}
	a.responderJSON(escritor, http.StatusOK, resposta)
}

// lerLimite interpreta o parametro de pagina, com padrao e teto.
//
// Valor invalido cai no padrao em vez de recusar a requisicao: este e um endpoint
// de diagnostico, digitado a mao por um tecnico, e recusar por causa de um erro de
// digitacao nao ajuda ninguem.
func (a *Apresentacao) lerLimite(requisicao *http.Request) int {
	bruto := requisicao.URL.Query().Get("limite")
	if bruto == "" {
		return leiturasPorPaginaPadrao
	}

	limite, err := strconv.Atoi(bruto)
	if err != nil || limite <= 0 {
		return leiturasPorPaginaPadrao
	}
	if limite > leiturasPorPaginaMaximo {
		return leiturasPorPaginaMaximo
	}
	return limite
}

func (a *Apresentacao) responderJSON(escritor http.ResponseWriter, status int, corpo any) {
	escritor.Header().Set("Content-Type", "application/json; charset=utf-8")
	escritor.WriteHeader(status)
	if err := json.NewEncoder(escritor).Encode(corpo); err != nil {
		a.registro.Debug("resposta nao chegou ao chamador", slog.String("erro", err.Error()))
	}
}
