// Package projecao move o dado do diario duravel para o banco de consulta.
//
// E o segundo estagio do sistema, e o unico que pode falhar sem consequencia para
// a integridade do dado. Toda decisao aqui e tomada com isso em vista: quando algo
// da errado, a resposta e PARAR e tentar de novo depois — nunca improvisar, nunca
// pular registro, nunca avancar o cursor sobre o que nao foi gravado.
package projecao

import (
	"context"
	"log/slog"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/saida/diariosqlite"
	"github.com/ViktorWalde/SynkaCore/internal/adaptador/saida/projetortimescale"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/estadooperacional"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/relogio"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/resiliencia"
)

const (
	// NomeDoCursor identifica este consumidor no diario.
	//
	// Nomeado, e nao implicito, porque o cursor foi desenhado para admitir mais de
	// um consumidor com avancos independentes — auditoria, exportacao, um segundo
	// destino analitico — sem que o diario ganhe uma coluna por consumidor.
	NomeDoCursor = "timescale"

	// RegistrosPorCiclo limita quanto cada ciclo arrasta do diario.
	//
	// Existe para que a retomada apos uma queda longa nao tente projetar horas de
	// dado numa transacao so: o lote gigante estouraria a memoria e, pior, um erro
	// no fim descartaria todo o trabalho.
	RegistrosPorCiclo = 500

	// IntervaloEntreCiclos e a cadencia normal da projecao.
	//
	// Dois segundos e o compromisso: mais curto desperdicaria consulta ao diario em
	// operacao ociosa, mais longo faria o dashboard parecer travado. Ele NAO afeta
	// a durabilidade — o dado ja esta salvo antes de a projecao existir.
	IntervaloEntreCiclos = 2 * time.Second
)

// Servico e o laco de projecao.
type Servico struct {
	diario     *diariosqlite.Diario
	projetor   *projetortimescale.Projetor
	catalogo   *aquisicao.CatalogoDeConteudo
	pipeline   *resiliencia.Pipeline
	rastreador *estadooperacional.Rastreador
	relogio    relogio.Relogio
	registro   *slog.Logger
}

// NovoServico constroi o laco.
func NovoServico(diario *diariosqlite.Diario, projetor *projetortimescale.Projetor,
	catalogo *aquisicao.CatalogoDeConteudo, pipeline *resiliencia.Pipeline,
	rastreador *estadooperacional.Rastreador, r relogio.Relogio, registro *slog.Logger) *Servico {

	return &Servico{
		diario: diario, projetor: projetor, catalogo: catalogo, pipeline: pipeline,
		rastreador: rastreador, relogio: r, registro: registro,
	}
}

// Executar roda a projecao ate o contexto ser cancelado.
//
// Um ciclo que falha NAO interrompe o laco. A projecao e feita para sobreviver a
// quedas prolongadas do banco de consulta sem intervencao — se ela morresse na
// primeira falha, alguem precisaria reinicia-la a mao, e "alguem" numa planta as
// tres da manha nao existe.
func (s *Servico) Executar(ctx context.Context) error {
	temporizador := time.NewTicker(IntervaloEntreCiclos)
	defer temporizador.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-temporizador.C:
			s.rodarCiclo(ctx)
		}
	}
}

// rodarCiclo projeta um lote e avanca o cursor.
func (s *Servico) rodarCiclo(ctx context.Context) {
	ultimoID, err := s.diario.LerCursor(ctx, NomeDoCursor)
	if err != nil {
		// Falha ao LER O DIARIO e categoria interna, e nao indisponivel: o diario e
		// local e nao deveria falhar. Ela nao passa pela pipeline de resiliencia
		// porque o disjuntor protege a dependencia remota, e esta nao e remota —
		// abrir o disjuntor aqui esconderia um defeito nosso atras de um estado que
		// significa "a rede caiu".
		s.registro.Error("nao foi possivel ler o cursor de projecao",
			slog.String("erro", err.Error()))
		return
	}

	registros, err := s.diario.LerAPartirDe(ctx, ultimoID, RegistrosPorCiclo)
	if err != nil {
		s.registro.Error("nao foi possivel ler o diario",
			slog.String("erro", err.Error()))
		return
	}
	if len(registros) == 0 {
		// Nada pendente e o estado normal em operacao saudavel. Ele tambem confirma
		// que a projecao alcancou a ingestao, entao vale marcar como conectado.
		s.rastreador.Notificar(estadooperacional.Conectado, s.relogio.Agora())
		return
	}

	linhas, ultimoProjetado := s.converter(registros)

	err = s.pipeline.Executar(ctx, s.relogio.Agora, func(ctx context.Context) error {
		return s.projetor.Projetar(ctx, linhas)
	})
	if err != nil {
		s.aoFalhar(err)
		return
	}

	// O cursor avanca DEPOIS de a gravacao estar confirmada. A ordem inversa
	// perderia o intervalo para sempre numa queda entre as duas operacoes; nesta
	// ordem, a queda apenas faz reprocessar — e reprocessar e inofensivo, porque a
	// gravacao e idempotente pela restricao de unicidade do modelo de leitura.
	if err := s.diario.AvancarCursor(ctx, NomeDoCursor, ultimoProjetado, s.relogio.Agora()); err != nil {
		// O dado JA esta no banco de consulta. Falhar aqui custa reprocessar o lote,
		// nunca o dado — e por isso isto e um aviso, e nao um alarme.
		s.registro.Warn("projecao gravada mas o cursor nao avancou: o lote sera reprocessado",
			slog.String("erro", err.Error()))
		return
	}

	s.rastreador.Notificar(estadooperacional.Conectado, s.relogio.Agora())
	s.registro.Debug("lote projetado",
		slog.Int("registros", len(registros)),
		slog.Int("linhas", len(linhas)),
		slog.Int64("cursor", ultimoProjetado))
}

// converter transforma registros do diario em linhas do modelo de leitura.
//
// Um registro que nao decodifica NAO interrompe o ciclo nem e pulado em silencio.
// Ele e registrado e o cursor avanca sobre ele — a alternativa seria travar a
// projecao para sempre no mesmo registro, e um unico byte corrompido pararia o
// dashboard da planta inteira indefinidamente.
//
// Este e o mesmo defeito que a auditoria da V1.2 encontrou no buffer da V1.x: uma
// leitura malformada era capturada, o ciclo parava, e a leitura nunca era marcada
// como sincronizada — entao todo ciclo futuro tentava o mesmo item e falhava no
// mesmo ponto. A correcao la e a mesma daqui, e o que a torna segura e que o
// registro bruto CONTINUA no diario: nada foi destruido, e ele pode ser
// reprocessado quando o defeito de decodificacao for corrigido.
func (s *Servico) converter(registros []diariosqlite.RegistroDoDiario) ([]projetortimescale.LinhaProjetada, int64) {
	linhas := make([]projetortimescale.LinhaProjetada, 0, len(registros)*4)
	var ultimoProjetado int64

	for _, registro := range registros {
		ultimoProjetado = registro.ID

		definicao, err := s.catalogo.Buscar(aquisicao.TipoDeConteudo(registro.TipoDeConteudo))
		if err != nil {
			s.registro.Warn("registro com tipo de conteudo desconhecido permanece no diario, sem projecao",
				slog.String("chave_de_idempotencia", registro.ChaveDeIdempotencia),
				slog.String("tipo_de_conteudo", registro.TipoDeConteudo))
			continue
		}

		conteudo, err := definicao.Decodificar(registro.ConteudoBruto)
		if err != nil {
			s.registro.Error("registro nao decodifica; permanece no diario para reprocessamento",
				slog.String("chave_de_idempotencia", registro.ChaveDeIdempotencia),
				slog.String("erro", err.Error()))
			continue
		}

		linhas = append(linhas, projetortimescale.LinhasDe(conteudo, projetortimescale.LinhaProjetada{
			InstanteObservado: registro.InstanteObservado,
			TempoLigadoMs:     registro.TempoLigado.Milliseconds(),
			IDDoDispositivo:   registro.IDDoDispositivo,
			IDDaSessaoDeBoot:  registro.IDDaSessaoDeBoot,
			NumeroDeSequencia: int64(registro.NumeroDeSequencia),
			TipoDeConteudo:    registro.TipoDeConteudo,
			ClasseDeDado:      registro.ClasseDeDado,
		})...)
	}

	return linhas, ultimoProjetado
}

// aoFalhar registra a falha e atualiza o estado operacional.
//
// A distincao entre reconectando e degradado nao e cosmetica: ela decide se alguem
// e acordado. Reconectando se resolve sozinho na maioria das vezes, e alarmar por
// isso e o caminho mais curto para o alarme ser ignorado quando importar.
func (s *Servico) aoFalhar(err error) {
	agora := s.relogio.Agora()

	if s.pipeline.Estado() == resiliencia.Aberto {
		s.rastreador.Notificar(estadooperacional.Degradado, agora)
	} else {
		s.rastreador.Notificar(estadooperacional.Reconectando, agora)
	}

	// O aviso e emitido a cada ciclo que falha, mas a TRANSICAO de estado e
	// registrada uma vez so pelo rastreador. E o que evita que uma queda de tres
	// horas produza milhares de linhas identicas no disco de um equipamento que
	// ninguem visita.
	s.registro.Warn("ciclo de projecao falhou; o dado permanece duravel no diario",
		slog.String("categoria", falha.CategoriaDe(err).String()),
		slog.String("disjuntor", s.pipeline.Estado().String()),
		slog.String("erro", err.Error()))
}
