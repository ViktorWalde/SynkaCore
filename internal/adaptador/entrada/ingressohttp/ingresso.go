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

	// EsperaDeContrapressaoPadrao acompanha o 429.
	//
	// A origem soma JITTER a este valor. Sem jitter, as origens sincronizam e a
	// frota inteira reconecta junta — a falha parcial vira total, e o gateway que
	// estava apenas lento cai de vez.
	EsperaDeContrapressaoPadrao = 2 * time.Second

	operacaoIngresso = "ingressohttp.Ingestao"
)

// Ingresso e o adaptador HTTP do caminho de aquisicao.
type Ingresso struct {
	servico  *ingestao.Servico
	catalogo *aquisicao.CatalogoDeConteudo
	relogio  relogio.Relogio
	registro *slog.Logger
}

// NovoIngresso constroi o adaptador.
func NovoIngresso(servico *ingestao.Servico, catalogo *aquisicao.CatalogoDeConteudo,
	r relogio.Relogio, registro *slog.Logger) *Ingresso {
	return &Ingresso{servico: servico, catalogo: catalogo, relogio: r, registro: registro}
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

	for indice, motivo := range decodificada.MotivosDaRejeicao {
		i.registro.Warn("envelope rejeitado definitivamente",
			slog.Uint64("numero_de_sequencia", decodificada.SequenciasRejeitadas[indice]),
			slog.String("motivo", motivo.Error()))
	}

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
		escritor.Header().Set("Retry-After",
			strconv.Itoa(int(EsperaDeContrapressaoPadrao.Seconds())))
	}
	escritor.Header().Set("Content-Type", "text/plain; charset=utf-8")
	escritor.WriteHeader(status)
	if _, err := escritor.Write([]byte(categoria.String())); err != nil {
		i.registro.Debug("resposta de falha nao chegou ao chamador")
	}
}
