// Package ingressohttp expoe o caminho de aquisicao pela rede.
//
// Este e o ponto do sistema que fala com o mundo nao confiavel. Toda decisao aqui
// assume que quem esta do outro lado pode ser hostil: limite de corpo antes de
// ler, limite de lote antes de decodificar, e validacao na largura em que o dado
// chegou.
package ingressohttp

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/codecdefio"
	"github.com/ViktorWalde/SynkaCore/internal/aplicacao/ingestao"
	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/contrapressao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/credencial"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/relogio"
)

const (
	// CaminhoDeIngestao e onde as origens entregam suas remessas.
	CaminhoDeIngestao = "/ingestao"

	// CaminhoDeSaude e a verificacao de saude do gateway.
	CaminhoDeSaude = "/saude"

	// TipoDeConteudoProtobuf e o formato do fio.
	TipoDeConteudoProtobuf = "application/x-protobuf"

	// TamanhoMaximoDoCorpo limita a requisicao ANTES de qualquer leitura.
	//
	// Um lote cheio de conteudos no limite nao chega perto disto; a folga existe
	// para nunca recusar remessa legitima. O que o limite fecha e o vetor de
	// exaustao de memoria por corpo infinito — uma origem comprometida nao derruba
	// a planta mandando bytes para sempre.
	TamanhoMaximoDoCorpo = 4 << 20 // 4 MiB

	operacaoIngresso = "ingressohttp.Ingestao"
)

// Ingresso e o adaptador HTTP do caminho de aquisicao.
type Ingresso struct {
	servico  *ingestao.Servico
	catalogo *aquisicao.CatalogoDeConteudo
	portaria *contrapressao.Portaria
	relogio  relogio.Relogio
	registro *slog.Logger

	// exigirIdentidadeAutenticada liga a confrontacao entre a identidade que a
	// remessa reivindica e a que o certificado prova.
	//
	// Desligada quando o ingresso roda sem TLS. Isso NAO e tolerancia frouxa: sem
	// TLS nao existe identidade provada para confrontar, e fingir que existe seria
	// pior que admitir que nao ha. O que o gateway faz nesse caso e AVISAR, alto e
	// na partida, que esta operando sem autenticacao.
	exigirIdentidadeAutenticada bool
}

// NovoIngresso constroi o adaptador.
//
// A portaria e parametro OBRIGATORIO, e nao um ajuste opcional encaixado depois
// por um metodo `Com...`. A distincao e a mesma que separa o diario de um buffer
// de emergencia: um mecanismo que so entra em acao quando alguem lembrou de
// liga-lo e um mecanismo que nao funciona. Ate a V2.3 o gateway tinha o mapeador
// de 429 pronto e nada que o acionasse — a contrapressao existia no papel, e a
// unica coisa que faltava era exatamente este parametro nao ser esquecivel.
func NovoIngresso(servico *ingestao.Servico, catalogo *aquisicao.CatalogoDeConteudo,
	portaria *contrapressao.Portaria, r relogio.Relogio, registro *slog.Logger) *Ingresso {
	return &Ingresso{servico: servico, catalogo: catalogo, portaria: portaria,
		relogio: r, registro: registro}
}

// ComIdentidadeAutenticada liga a confrontacao de identidade.
//
// Chamado pela raiz de composicao quando o ingresso sobe em TLS com certificado de
// cliente exigido.
func (i *Ingresso) ComIdentidadeAutenticada(exigir bool) *Ingresso {
	i.exigirIdentidadeAutenticada = exigir
	return i
}

// Rotas devolve o multiplexador com o caminho de aquisicao montado.
func (i *Ingresso) Rotas() *http.ServeMux {
	rotas := http.NewServeMux()
	rotas.HandleFunc("POST "+CaminhoDeIngestao, i.receberRemessa)
	return rotas
}

// receberRemessa e o handler do caminho de aquisicao.
func (i *Ingresso) receberRemessa(escritor http.ResponseWriter, requisicao *http.Request) {
	// O limite e imposto ANTES da leitura, e nao verificado depois: verificar
	// depois exigiria ja ter lido o corpo inteiro, que e exatamente o que o limite
	// existe para impedir.
	corpoLimitado := http.MaxBytesReader(escritor, requisicao.Body, TamanhoMaximoDoCorpo)

	bruto, err := io.ReadAll(corpoLimitado)
	if err != nil {
		var excedeu *http.MaxBytesError
		if errors.As(err, &excedeu) {
			i.responderFalha(escritor, falha.Nova(falha.CategoriaEntradaInvalida,
				operacaoIngresso, "remessa excede o tamanho maximo do corpo"))
			return
		}
		i.responderFalha(escritor, falha.Envolver(falha.CategoriaEntradaInvalida,
			operacaoIngresso, "nao foi possivel ler o corpo da remessa", err))
		return
	}

	// O carimbo de recepcao e do GATEWAY, tomado aqui, o mais perto possivel da
	// chegada. A origem nunca afirma saber a hora.
	instanteObservado := i.relogio.Agora()

	decodificada, err := codecdefio.DecodificarRemessa(bruto, instanteObservado, i.catalogo)
	if err != nil {
		i.responderFalha(escritor, err)
		return
	}

	// A CONFRONTACAO DE IDENTIDADE ACONTECE AQUI, e nao no codec.
	//
	// Quem prova identidade e o transporte, e so este adaptador tem acesso a
	// credencial. Fazer a conferencia dentro do codec exigiria passar a identidade
	// autenticada para dentro dele — espalhando decisao de seguranca por uma camada
	// que nao deveria tomar nenhuma.
	//
	// E acontece ANTES da ingestao: uma remessa que reivindica identidade alheia nao
	// pode chegar ao diario nem por um instante.
	if err := i.conferirIdentidade(requisicao, decodificada.IDDoDispositivoReivindicado); err != nil {
		i.responderFalha(escritor, err)
		return
	}

	for indice, motivo := range decodificada.MotivosDaRejeicao {
		i.registro.Warn("envelope rejeitado definitivamente",
			slog.Uint64("numero_de_sequencia", decodificada.SequenciasRejeitadas[indice]),
			slog.String("motivo", motivo.Error()))
	}

	// A ADMISSAO ACONTECE AQUI: depois de decodificar, antes de gravar.
	//
	// Depois de decodificar porque a decisao depende da CLASSE do que veio, e a
	// classe so existe depois da decodificacao. Recusar antes seria recusar no
	// escuro, tratando uma parada de maquina como uma leitura de temperatura.
	//
	// O custo dessa ordem foi medido na V2.3 e e o argumento que a sustenta:
	// decodificar e validar custa 1,9 us por envelope, e gravar custa 33 us. Uma
	// remessa recusada joga fora ~190 us de decodificacao para evitar ~3,3 ms de
	// gravacao numa fila que ja nao da conta — dezessete vezes mais barato que
	// admiti-la.
	//
	// E antes de gravar porque recusar depois nao seria contrapressao: seria
	// pagar o congestionamento inteiro e ainda assim nao entregar o dado.
	passagem, err := i.portaria.Entrar(requisicao.Context(),
		urgenciaDaRemessa(decodificada.Envelopes))
	if err != nil {
		i.responderFalha(escritor, err)
		return
	}
	defer passagem.Sair()

	confirmacao, err := i.servico.Ingerir(requisicao.Context(),
		decodificada.Envelopes, decodificada.SequenciasRejeitadas)
	if err != nil {
		i.responderFalha(escritor, err)
		return
	}

	if confirmacao.TempoSuspeito {
		// Registrado como erro, e nao como aviso: numa trilha que precisa provar
		// QUANDO algo aconteceu, um relogio que deu degrau e problema de
		// conformidade, nao ruido operacional.
		i.registro.Error("degrau de relogio detectado: a serie desta sessao de boot nao e temporalmente confiavel",
			slog.String("id_do_dispositivo", decodificada.Envelopes[0].IDDoDispositivo().String()),
			slog.String("id_da_sessao_de_boot", decodificada.Envelopes[0].IDDaSessaoDeBoot().String()))
	}

	i.responderConfirmacao(escritor, confirmacao)
}

// conferirIdentidade confronta o que a remessa reivindica com o que o TLS provou.
//
// Sem TLS, nao ha o que confrontar e a funcao passa. Isso e registrado uma vez na
// partida do gateway, e nao a cada remessa: um aviso por mensagem inundaria o log e
// deixaria de ser lido justamente por ser constante.
func (i *Ingresso) conferirIdentidade(requisicao *http.Request, reivindicada string) error {
	if !i.exigirIdentidadeAutenticada {
		return nil
	}

	autenticada, err := credencial.IdentidadeAutenticada(requisicao.TLS)
	if err != nil {
		return err
	}

	if err := credencial.ConferirIdentidade(autenticada, reivindicada); err != nil {
		// Registrado como ERRO, e nao como aviso. Isto e ou um defeito de
		// configuracao — dois nos com o mesmo certificado, ou o identificador
		// trocado no arquivo do no — ou uma tentativa de se passar por outro
		// dispositivo. Nenhum dos dois e ruido operacional.
		i.registro.Error("identidade reivindicada nao confere com a autenticada",
			slog.String("autenticada", autenticada.String()),
			slog.String("reivindicada", reivindicada))
		return err
	}
	return nil
}

// responderConfirmacao devolve a confirmacao no formato do contrato.
func (i *Ingresso) responderConfirmacao(escritor http.ResponseWriter, confirmacao ingestao.Confirmacao) {
	resposta := &contratov1.ConfirmacaoDeRemessa{
		DuravelAteASequencia:                proto.Uint64(confirmacao.DuravelAteASequencia),
		SequenciasRejeitadasDefinitivamente: confirmacao.SequenciasRejeitadas,
	}

	bytes, err := proto.Marshal(resposta)
	if err != nil {
		// O dado JA esta duravel neste ponto. Falhar aqui custa uma retransmissao,
		// nunca o dado — e por isso o log e o que importa, nao o status.
		i.registro.Error("falha ao serializar a confirmacao de remessa",
			slog.String("erro", err.Error()))
		i.responderFalha(escritor, falha.Envolver(falha.CategoriaInterna,
			operacaoIngresso, "falha ao serializar a confirmacao", err))
		return
	}

	escritor.Header().Set("Content-Type", TipoDeConteudoProtobuf)
	escritor.WriteHeader(http.StatusOK)
	if _, err := escritor.Write(bytes); err != nil {
		i.registro.Warn("confirmacao nao chegou a origem: a remessa sera retransmitida",
			slog.String("erro", err.Error()))
	}
}

// responderFalha traduz uma falha do dominio para a resposta HTTP.
//
// O corpo NAO carrega a mensagem interna do erro. Ela vai para o log, onde o
// operador a le; devolve-la a rede daria a um atacante um mapa do que o gateway
// valida e como. O status ja diz a unica coisa que a origem precisa saber:
// descartar (4xx) ou retransmitir (5xx).
func (i *Ingresso) responderFalha(escritor http.ResponseWriter, err error) {
	categoria := falha.CategoriaDe(err)
	status := statusDe(categoria)

	if status >= http.StatusInternalServerError {
		i.registro.Error("falha no caminho de aquisicao",
			slog.String("categoria", categoria.String()),
			slog.String("erro", err.Error()))
	} else {
		i.registro.Warn("remessa recusada",
			slog.String("categoria", categoria.String()),
			slog.String("erro", err.Error()))
	}

	if categoria == falha.CategoriaRecursoEsgotado {
		escritor.Header().Set("Retry-After", segundosDeEspera(i.portaria.EsperaSugerida()))
	}
	escritor.Header().Set("Content-Type", "text/plain; charset=utf-8")
	escritor.WriteHeader(status)
	if _, err := escritor.Write([]byte(categoria.String())); err != nil {
		i.registro.Debug("resposta de falha nao chegou ao chamador")
	}
}

// segundosDeEspera formata a espera para o cabecalho Retry-After.
//
// ARREDONDA PARA CIMA, e nunca devolve zero. Retry-After tem resolucao de
// segundos, e uma estimativa de 300 ms truncada viraria "0" — a origem voltaria na
// hora, o recuo deixaria de recuar exatamente quando ele existe para atuar, e o
// gateway saturado receberia a mesma carga de volta imediatamente.
//
// O jitter NAO e aplicado aqui, de proposito. Ele pertence a origem: o gateway
// manda o mesmo numero para a frota inteira, e se ele proprio o espalhasse, todas
// as origens ainda assim receberiam valores sorteados da mesma distribuicao no
// mesmo instante. Quem precisa se dessincronizar e quem vai voltar.
func segundosDeEspera(espera time.Duration) string {
	segundos := int(espera / time.Second)
	if espera%time.Second > 0 {
		segundos++
	}
	if segundos < 1 {
		segundos = 1
	}
	return strconv.Itoa(segundos)
}
