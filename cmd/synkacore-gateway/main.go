// Command synkacore-gateway e o processo do gateway do SynkaCore.
//
// Este arquivo e a RAIZ DE COMPOSICAO: o unico lugar do sistema que decide quais
// implementacoes concretas existem e como elas se ligam. Nenhum package de dominio
// ou de aplicacao constroi suas proprias dependencias.
//
// A concentracao e deliberada. Quando o wiring esta espalhado, a mesma dependencia
// acaba construida em varios lugares com configuracoes levemente diferentes — a
// duplicacao mais dificil de enxergar, porque cada ocorrencia isolada parece
// perfeitamente correta.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/entrada/apresentacaohttp"
	"github.com/ViktorWalde/SynkaCore/internal/adaptador/entrada/configuracaoarquivo"
	"github.com/ViktorWalde/SynkaCore/internal/adaptador/entrada/ingressohttp"
	"github.com/ViktorWalde/SynkaCore/internal/adaptador/entrada/servidordetempo"
	"github.com/ViktorWalde/SynkaCore/internal/adaptador/saida/diariosqlite"
	"github.com/ViktorWalde/SynkaCore/internal/adaptador/saida/projetortimescale"
	"github.com/ViktorWalde/SynkaCore/internal/aplicacao/ingestao"
	"github.com/ViktorWalde/SynkaCore/internal/aplicacao/projecao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/estadooperacional"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/instalacao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/contrapressao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/credencial"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/identificador"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/relogio"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/resiliencia"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/versao"
)

// Padroes dimensionados para o cenario alvo, todos sobrescreviveis.
//
// Nenhum limite de projeto e constante embutida: uma planta menor roda o mesmo
// binario com outros parametros, e nao com outra compilacao.
const (
	enderecoDeIngressoPadrao     = "127.0.0.1:8443"
	enderecoDeApresentacaoPadrao = "127.0.0.1:8080"
	caminhoDoDiarioPadrao        = "diario/synkacore.db"

	// bancoDeConsultaPadrao e vazio de proposito: sem ele, o gateway sobe COMPLETO
	// para aquisicao e apenas nao projeta.
	//
	// Isso e possivel porque o banco de consulta nao e o registro autoritativo. Um
	// gateway que se recusasse a subir sem ele transformaria uma dependencia de
	// APRESENTACAO em pre-requisito de AQUISICAO — e o caminho de aquisicao e o
	// unico que nunca pode parar.
	bancoDeConsultaPadrao = ""

	// instalacaoPadrao e vazio de proposito, pela mesma razao do banco: sem
	// configuracao, o gateway adquire e projeta normalmente — apenas sem saber o
	// que os canais significam.
	//
	// Isso e o estado NORMAL antes do comissionamento. Exigir configuracao para
	// operar faria o gateway ficar mudo justamente na fase em que se quer verificar
	// se ele funciona, e um sistema que so liga depois de configurado por completo
	// nao pode ser comissionado por etapas.
	instalacaoPadrao = ""

	// intervaloDePodaPadrao e a frequencia da limpeza do diario.
	intervaloDePodaPadrao = time.Hour

	// tetoDoDiarioPadrao limita quanto o diario pode ocupar em disco.
	//
	// Ele existe porque a PODA NAO BASTA: ela so remove o que ja foi projetado e
	// envelheceu, e portanto nao remove nada quando nao ha projecao configurada — que
	// e a configuracao recomendada para comissionar e para operar sem infraestrutura
	// analitica. Sem teto, um gateway deixado "so adquirindo" enche o disco.
	//
	// Dois gibibytes e folgado para o cenario alvo e conservador para o hardware: num
	// mini PC de planta com 32 GB, o diario nunca passa de 6% do disco. Uma origem a
	// 1 Hz com quatro canais leva meses para alcanca-lo; 200 origens a 100/s levam
	// horas — e nas duas pontas a recusa acontece antes de a maquina parar.
	tetoDoDiarioPadrao = 2 << 30

	// intervaloDeOcupacaoPadrao e a frequencia com que o tamanho e reconferido.
	//
	// Dez segundos, e nao a cada remessa: a 20.000 envelopes/s o diario cresce alguns
	// megabytes por segundo, e a margem de erro dessa janela cabe folgada dentro do
	// teto. O caminho quente paga uma leitura atomica em vez de um os.Stat.
	intervaloDeOcupacaoPadrao = 10 * time.Second

	// retencaoDoDiarioPadrao e quanto tempo um registro ja projetado permanece.
	//
	// Sete dias e a janela que permite reprocessar quando um erro de projecao e
	// descoberto dias depois. Apagar assim que projetado tornaria esse erro
	// irrecuperavel.
	retencaoDoDiarioPadrao = 7 * 24 * time.Hour

	// tempoLimiteDoDesligamento e quanto os servidores esperam pelas requisicoes em
	// curso antes de encerrar a forca.
	tempoLimiteDoDesligamento = 15 * time.Second

	// tempoLimiteDeLeituraDoCabecalho fecha o vetor de conexao lenta, em que um
	// cliente abre a conexao e envia o cabecalho byte a byte para segurar um
	// handler indefinidamente.
	tempoLimiteDeLeituraDoCabecalho = 10 * time.Second

	// amostrasDaCalibracao e quantas transacoes a partida mede para semear a
	// portaria.
	//
	// Vinte da mediana estavel sem atrasar a partida de forma perceptivel: mesmo a
	// ~1 ms por transacao — o pior caso medido na V2.3, lote unitario em disco real
	// — sao ~20 ms, contra os 15 s de tempo limite de desligamento do processo.
	//
	// Mais amostras nao comprariam precisao util: o numero e um PISO destinado a
	// preencher a lacuna ate a media movel receber gravacoes de verdade, e nao uma
	// previsao do custo de uma remessa.
	amostrasDaCalibracao = 20

	// tempoLimiteDaCalibracao impede que um disco doente segure a partida.
	//
	// Se ele nao for suficiente, a calibracao e ABANDONADA e o gateway sobe com a
	// portaria sem semente — exatamente o comportamento da V2.4. Ficar preso aqui
	// deixaria a planta sem aquisicao por causa de um refinamento.
	tempoLimiteDaCalibracao = 10 * time.Second
)

func main() {
	if err := executar(); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "synkacore-gateway: %v\n", err)
		os.Exit(1)
	}
}

// executar existe separado de main para que toda saida de erro passe por um
// caminho so. main nao contem logica; ela apenas traduz erro em codigo de saida.
func executar() error {
	enderecoDeIngresso := flag.String("ingresso", enderecoDeIngressoPadrao,
		"endereco de escuta do caminho de aquisicao (lado de chao de fabrica)")
	enderecoDeApresentacao := flag.String("apresentacao", enderecoDeApresentacaoPadrao,
		"endereco de escuta de consulta e saude (lado de escritorio)")
	caminhoDoDiario := flag.String("diario", caminhoDoDiarioPadrao,
		"caminho do arquivo do diario de ingestao")
	tetoDoDiario := flag.Int64("diario-maximo", tetoDoDiarioPadrao,
		"teto de bytes do diario; ao alcanca-lo o gateway recusa remessa em vez de encher o disco. Zero desliga")
	bancoDeConsulta := flag.String("banco", bancoDeConsultaPadrao,
		"URL do TimescaleDB para o modelo de leitura; vazio desliga a projecao")
	caminhoDaInstalacao := flag.String("instalacao", instalacaoPadrao,
		"arquivo de configuracao da instalacao; vazio deixa os canais sem significado")
	enderecoDoTempo := flag.String("tempo", "",
		"endereco UDP do servidor de tempo para as origens; vazio desliga")
	caminhoDaCA := flag.String("ca", "", "certificado da CA da instalacao; liga mTLS no ingresso")
	caminhoDoCertificado := flag.String("certificado", "", "certificado de servidor do gateway")
	caminhoDaChave := flag.String("chave", "", "chave privada do gateway")
	nivelDeRegistro := flag.String("registro", "info", "nivel de registro: debug, info, warn, error")
	mostrarVersao := flag.Bool("versao", false, "imprime a versao e sai")
	flag.Parse()

	// Respondida ANTES de qualquer efeito colateral — antes de abrir o diario, de
	// carregar configuracao ou de escutar em porta nenhuma. Um tecnico em campo
	// perguntando a versao nao pode, com isso, criar um arquivo de diario nem
	// disputar a porta com o servico que ja esta rodando.
	if *mostrarVersao {
		fmt.Println(versao.Completa())
		return nil
	}

	registro := montarRegistro(*nivelDeRegistro)

	// Identifica ESTA partida do processo. A leitura monotonica so tem sentido
	// dentro do processo que a produziu, e sem distinguir execucoes uma ancora
	// gravada antes de um reinicio seria comparada com o monotonico da execucao
	// atual — a diferenca entre duas contagens sem relacao apareceria como um
	// degrau de relogio gigante e completamente falso.
	idDaExecucao := identificador.Sortear("exec-")

	ctx, encerrar := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer encerrar()

	relogioDoSistema := relogio.Sistema()

	catalogo, err := montarCatalogo()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(*caminhoDoDiario), 0o750); err != nil {
		return fmt.Errorf("nao foi possivel criar a pasta do diario: %w", err)
	}
	diario, err := diariosqlite.Abrir(ctx, *caminhoDoDiario)
	if err != nil {
		return err
	}
	defer func() { _ = diario.Fechar() }()
	diario = diario.ComTetoDeBytes(*tetoDoDiario)

	// A primeira medicao acontece ANTES de o servidor atender.
	//
	// Sem ela, um gateway que reinicia com o diario ja acima do teto aceitaria
	// remessa ate o primeiro ciclo do laco — dez segundos gravando exatamente o que o
	// teto existe para impedir, e justamente quando a frota inteira reconecta.
	if _, err := diario.MedirOcupacao(); err != nil {
		registro.Warn("nao foi possivel medir a ocupacao inicial do diario",
			slog.String("erro", err.Error()))
	}

	rastreador := estadooperacional.NovoRastreador(relogioDoSistema.Agora(),
		func(anterior, atual estadooperacional.Estado) {
			// Registrado apenas na TRANSICAO, e nao a cada ciclo: um log que repete
			// a mesma linha a cada dois segundos durante uma queda de tres horas
			// enterra a informacao util e enche o disco de um equipamento que
			// ninguem visita.
			registro.Warn("estado da projecao mudou",
				slog.String("anterior", anterior.String()),
				slog.String("atual", atual.String()))
		})

	// A configuracao e carregada ANTES de qualquer coisa que dependa dela, e falha
	// derruba a partida: um erro de digitacao num mapeamento de canal produziria
	// dado atribuido ao ponto errado, em silencio, por meses.
	configuracao, err := carregarInstalacao(*caminhoDaInstalacao, registro)
	if err != nil {
		return err
	}

	// A ORDEM DESTAS TRES ETAPAS E A DECISAO, e ela separa politica de medicao.
	//
	//  1. A POLITICA vem da instalacao — quanto cada classe de dado tolera esperar.
	//     E uma promessa sobre o dado, e nenhum disco a altera.
	//  2. A portaria e construida com ela, ANTES dos dois adaptadores: o ingresso
	//     admite por ela, a apresentacao apenas a relata. Uma portaria por adaptador
	//     daria dois numeros para a mesma fila, e o /saude descreveria uma saturacao
	//     diferente da que esta recusando.
	//  3. A MEDICAO vem do disco, e so semeia o custo. Ela nao toca na politica.
	//
	// Inverter isso — derivar o orcamento do disco — faria a promessa se acomodar ao
	// pior hardware em silencio, e um limite que se ajusta ao que o encosta deixou de
	// ser um limite.
	admissao := instalacao.AdmissaoPadrao()
	if configuracao != nil {
		admissao = configuracao.Admissao()
	}
	portaria := contrapressao.NovaPortaria(
		ingressohttp.AjustesDaPortaria(admissao), relogioDoSistema.Decorrido)

	calibrarAdmissao(ctx, diario, portaria, admissao, registro)

	apresentacao := apresentacaohttp.NovaApresentacao(diario, catalogo, rastreador,
		relogioDoSistema, registro).ComInstalacao(configuracao).ComContrapressao(portaria)

	// O relatorio de comissionamento e montado do diario ANTES de o servidor
	// atender, e atualizado periodicamente depois.
	//
	// Uma unica fonte para os dois casos, de proposito. A primeira versao alimentava
	// o relatorio pela PROJECAO, e isso o deixava vazio sempre que nao havia banco
	// de consulta configurado — justamente a situacao do comissionamento, que
	// acontece ANTES de existir infraestrutura analitica. Duas fontes para o mesmo
	// estado tambem divergiriam.
	atualizarComissionamento(ctx, diario, catalogo, apresentacao, registro)
	go manterComissionamentoAtualizado(ctx, diario, catalogo, apresentacao, registro)

	projetor, err := ligarProjecao(ctx, *bancoDeConsulta, diario, catalogo, configuracao,
		rastreador, relogioDoSistema, registro)
	if err != nil {
		return err
	}
	if projetor != nil {
		defer projetor.Fechar()
	}
	apresentacao = apresentacao.ComProjecaoLigada(projetor != nil)

	material := credencial.Material{
		CaminhoDaCA:          *caminhoDaCA,
		CaminhoDoCertificado: *caminhoDoCertificado,
		CaminhoDaChave:       *caminhoDaChave,
	}
	tlsDoIngresso, err := montarTLSDoIngresso(material, registro)
	if err != nil {
		return err
	}

	servicoDeIngestao := ingestao.NovoServico(diario, relogioDoSistema, idDaExecucao)
	ingresso := ingressohttp.NovoIngresso(servicoDeIngestao, catalogo, portaria,
		relogioDoSistema, registro).
		ComIdentidadeAutenticada(tlsDoIngresso != nil)

	// DOIS servidores, e nao dois caminhos no mesmo multiplexador.
	//
	// O gateway fica entre duas redes: a de chao de fabrica, so com origens que nos
	// provisionamos, e a de escritorio, do cliente, com estacoes que nao
	// administramos. Um unico servidor pareceria mais simples e destruiria a
	// separacao — bastaria publicar a porta errada para o caminho de aquisicao
	// ficar exposto ao escritorio.
	servidorDeIngresso := montarServidor(*enderecoDeIngresso, ingresso.Rotas())
	servidorDeIngresso.TLSConfig = tlsDoIngresso
	servidorDeApresentacao := montarServidor(*enderecoDeApresentacao, apresentacao.Rotas())

	registro.Info("synkacore-gateway iniciando",
		slog.String("versao", versao.Numero),
		slog.String("id_da_execucao", idDaExecucao),
		slog.String("ingresso", *enderecoDeIngresso),
		slog.String("apresentacao", *enderecoDeApresentacao),
		slog.String("diario", *caminhoDoDiario),
		slog.Int64("teto_do_diario_bytes", *tetoDoDiario),
		slog.Bool("projecao_ligada", projetor != nil),
		slog.Bool("instalacao_configurada", configuracao != nil),
		slog.Bool("ingresso_autenticado", tlsDoIngresso != nil),
		slog.Duration("espera_maxima_da_amostra", admissao.OrcamentoDaAmostra),
		slog.Duration("espera_maxima_do_evento", admissao.OrcamentoDoEventoDiscreto),
		slog.Int("tipos_de_conteudo", len(catalogo.Tipos())))

	falhaDeServidor := make(chan error, 2)
	go servir(servidorDeIngresso, "ingresso", falhaDeServidor)
	go servir(servidorDeApresentacao, "apresentacao", falhaDeServidor)
	go podarPeriodicamente(ctx, diario, relogioDoSistema, registro)
	go vigiarOcupacaoDoDiario(ctx, diario, registro)
	ligarServidorDeTempo(ctx, *enderecoDoTempo, relogioDoSistema, registro)

	select {
	case err := <-falhaDeServidor:
		encerrar()
		desligar(servidorDeIngresso, servidorDeApresentacao, registro)
		return err
	case <-ctx.Done():
		registro.Info("sinal de desligamento recebido")
		desligar(servidorDeIngresso, servidorDeApresentacao, registro)
		return nil
	}
}

// montarCatalogo declara os tipos de conteudo que este gateway reconhece.
//
// A lista vive em aquisicao.TodasAsDefinicoes, e nao aqui, para que o teste que
// confere cobertura contra o contrato leia a MESMA lista. Duas listas do mesmo
// conjunto divergem, e a que o autor esquece de atualizar e sempre a do teste.
func montarCatalogo() (*aquisicao.CatalogoDeConteudo, error) {
	return aquisicao.NovoCatalogoDeConteudo(aquisicao.TodasAsDefinicoes()...)
}

// ligarProjecao conecta ao banco de consulta e inicia o laco, quando configurado.
//
// Devolve nil, sem erro, quando o banco nao foi informado. Isso e deliberado e nao
// e tolerancia frouxa: em desenvolvimento e no comissionamento inicial o gateway
// precisa ser exercitavel sem infraestrutura analitica, e o que ele entrega — dado
// duravel e nao perdido — nao depende dela em nada.
//
// Falhar ao ABRIR uma URL informada, ao contrario, e erro: se alguem configurou o
// banco, subir em silencio sem projetar esconderia justamente o problema que
// precisa ser visto na instalacao.
func ligarProjecao(ctx context.Context, urlDoBanco string, diario *diariosqlite.Diario,
	catalogo *aquisicao.CatalogoDeConteudo, configuracao *instalacao.Instalacao,
	rastreador *estadooperacional.Rastreador, r relogio.Relogio,
	registro *slog.Logger) (*projetortimescale.Projetor, error) {

	if urlDoBanco == "" {
		registro.Warn("projecao desligada: nenhum banco de consulta configurado. " +
			"A aquisicao funciona normalmente e o dado fica duravel no diario")
		return nil, nil
	}

	projetor, err := projetortimescale.Abrir(ctx, urlDoBanco)
	if err != nil {
		return nil, err
	}
	if err := projetor.Verificar(ctx); err != nil {
		// A verificacao acontece na PARTIDA, e nao no primeiro ciclo, porque banco
		// sem o esquema aplicado e erro de instalacao: descobri-lo agora custa uma
		// mensagem clara, descobri-lo depois custa uma investigacao.
		projetor.Fechar()
		return nil, err
	}

	pipeline := resiliencia.NovaPipeline(resiliencia.AjustesPadrao(),
		func(anterior, atual resiliencia.EstadoDoDisjuntor) {
			registro.Warn("disjuntor do banco de consulta mudou de estado",
				slog.String("anterior", anterior.String()),
				slog.String("atual", atual.String()))
		},
		rand.Float64)

	servico := projecao.NovoServico(diario, projetor, catalogo, pipeline, rastreador, r, registro).
		ComInstalacao(configuracao)
	go func() {
		if err := servico.Executar(ctx); err != nil && !errors.Is(err, context.Canceled) {
			registro.Error("laco de projecao encerrou", slog.String("erro", err.Error()))
		}
	}()

	return projetor, nil
}

// calibrarAdmissao mede o disco do diario e semeia a portaria.
//
// POR QUE MEDIR NA PARTIDA. A portaria estima a espera multiplicando o custo medio
// de uma gravacao pela fila, e ate a V2.4 esse custo comecava em zero: sem nenhuma
// remessa concluida a estimativa era zero, cabia em qualquer orcamento, e a
// admissao nao tinha com que decidir. A degradacao era segura e valia exatamente no
// pior momento — logo apos um reinicio, quando a frota inteira reconecta com o
// buffer cheio.
//
// FALHA AQUI NAO DERRUBA NADA, e a assimetria e a mesma do servidor de tempo e do
// relatorio de comissionamento: o gateway sobe sem semente, que e o comportamento
// da V2.4, e a media movel aprende com as primeiras remessas. Recusar-se a operar
// por causa de um refinamento deixaria a planta sem AQUISICAO — e a aquisicao e o
// unico caminho que nunca pode parar.
//
// O numero e registrado ao lado do orcamento de proposito. Juntos eles respondem a
// unica pergunta que um operador faz quando ve recusa alta: o disco e lento, ou o
// orcamento e apertado? Separados, ela nao tem resposta.
func calibrarAdmissao(ctx context.Context, diario *diariosqlite.Diario,
	portaria *contrapressao.Portaria, admissao instalacao.Admissao, registro *slog.Logger) {

	ctxDaCalibracao, cancelar := context.WithTimeout(ctx, tempoLimiteDaCalibracao)
	defer cancelar()

	custo, err := diario.MedirCustoDeTransacao(ctxDaCalibracao, amostrasDaCalibracao)
	if err != nil {
		registro.Warn("calibracao do disco falhou: a admissao comeca sem custo conhecido e "+
			"aprende com as primeiras remessas",
			slog.String("erro", err.Error()))
		return
	}

	portaria.Semear(custo)

	// Quantas remessas cabem no orcamento da amostra ANTES de a recusa comecar. E a
	// traducao do custo para a unidade em que o operador pensa — origens, e nao
	// microssegundos.
	folga := int(admissao.OrcamentoDaAmostra / custo)

	registro.Info("disco do diario calibrado",
		slog.Duration("custo_por_transacao", custo),
		slog.Duration("espera_maxima_da_amostra", admissao.OrcamentoDaAmostra),
		slog.Int("remessas_na_fila_antes_de_recusar_amostra", folga))
}

// intervaloDeComissionamento e a cadencia de atualizacao do relatorio.
//
// Trinta segundos e folgado: as origens reenviam descritor a cada cinco minutos, e
// o gargalo de responsividade e esse, nao esta consulta. A consulta e indexada e
// devolve uma linha por dispositivo, entao o custo e desprezivel mesmo numa planta
// com centenas de origens.
const intervaloDeComissionamento = 30 * time.Second

// manterComissionamentoAtualizado relê o diario periodicamente.
func manterComissionamentoAtualizado(ctx context.Context, diario *diariosqlite.Diario,
	catalogo *aquisicao.CatalogoDeConteudo, apresentacao *apresentacaohttp.Apresentacao,
	registro *slog.Logger) {

	temporizador := time.NewTicker(intervaloDeComissionamento)
	defer temporizador.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-temporizador.C:
			atualizarComissionamento(ctx, diario, catalogo, apresentacao, registro)
		}
	}
}

// atualizarComissionamento repovoa o relatorio a partir do diario.
//
// Falha aqui NAO derruba nada, e a assimetria e deliberada: o relatorio e
// diagnostico, e recusar-se a subir por causa dele deixaria a planta sem AQUISICAO
// por causa de uma funcionalidade acessoria. O erro e registrado e o relatorio se
// preenche na proxima tentativa.
func atualizarComissionamento(ctx context.Context, diario *diariosqlite.Diario,
	catalogo *aquisicao.CatalogoDeConteudo, apresentacao *apresentacaohttp.Apresentacao,
	registro *slog.Logger) {

	definicao, err := catalogo.Buscar(aquisicao.TipoDescritorDaOrigem)
	if err != nil {
		registro.Error("catalogo sem definicao de descritor", slog.String("erro", err.Error()))
		return
	}

	gravados, err := diario.UltimosDescritores(ctx, string(aquisicao.TipoDescritorDaOrigem))
	if err != nil {
		registro.Warn("nao foi possivel atualizar o relatorio de comissionamento",
			slog.String("erro", err.Error()))
		return
	}

	for _, gravado := range gravados {
		conteudo, err := definicao.Decodificar(gravado.ConteudoBruto)
		if err != nil {
			registro.Warn("descritor gravado nao decodifica",
				slog.String("id_do_dispositivo", gravado.IDDoDispositivo),
				slog.String("erro", err.Error()))
			continue
		}
		if descritor, ehDescritor := conteudo.(aquisicao.DescritorDaOrigem); ehDescritor {
			apresentacao.RegistrarDescritor(gravado.IDDoDispositivo, descritor, gravado.InstanteObservado)
		}
	}
}

// ligarServidorDeTempo sobe o relogio da rede de chao de fabrica, quando configurado.
//
// Ele existe para uma unica finalidade: permitir que uma origem embarcada VALIDE o
// certificado do gateway. Sem relogio de bateria, ela nasce em 1970 e a validacao
// falha — e a alternativa seria o no nao autenticar o gateway, aceitando qualquer
// impostor que atenda naquele endereco.
//
// Isso NAO afeta a autoridade de tempo do dado: o no continua reportando apenas
// tempo monotonico, e o gateway continua sendo quem ancora. Sao dois usos distintos
// do relogio, e confundi-los seria o erro.
//
// Falha ao subir NAO derruba o gateway: a porta 123 exige privilegio em muitos
// sistemas, e recusar-se a operar por causa disso deixaria a planta sem aquisicao
// por causa de um servico acessorio.
func ligarServidorDeTempo(ctx context.Context, endereco string,
	r relogio.Relogio, registro *slog.Logger) {

	if endereco == "" {
		return
	}

	servidor := servidordetempo.Novo(r, registro)
	go func() {
		if err := servidor.Escutar(ctx, endereco); err != nil && !errors.Is(err, context.Canceled) {
			registro.Error("servidor de tempo nao subiu; origens embarcadas nao conseguirao "+
				"validar o certificado do gateway",
				slog.String("endereco", endereco),
				slog.String("erro", err.Error()))
		}
	}()
}

// carregarInstalacao le a configuracao da planta, quando informada.
//
// Devolve nil, sem erro, quando nenhum caminho foi dado — o gateway opera sem saber
// o que os canais significam, que e o estado normal antes do comissionamento.
//
// Falhar ao carregar um caminho INFORMADO, ao contrario, derruba a partida. Se
// alguem apontou um arquivo, subir em silencio sem aplica-lo esconderia exatamente
// o problema que precisa ser visto — e o gateway rodaria atribuindo dado a nenhum
// ponto de medicao, parecendo funcionar.
func carregarInstalacao(caminho string, registro *slog.Logger) (*instalacao.Instalacao, error) {
	if caminho == "" {
		registro.Warn("nenhuma instalacao configurada: os canais serao gravados sem ponto de medicao, " +
			"grandeza ou unidade. Use -instalacao para dar significado ao dado")
		return nil, nil
	}

	configuracao, err := configuracaoarquivo.Carregar(caminho)
	if err != nil {
		return nil, err
	}

	registro.Info("instalacao carregada",
		slog.String("id_da_instalacao", configuracao.ID()),
		slog.Int("canais_configurados", len(configuracao.CanaisConfigurados())),
		slog.Uint64("versao_do_catalogo_de_motivos", uint64(configuracao.VersaoDoCatalogoDeMotivos())))
	return configuracao, nil
}

// montarServidor aplica os tempos limite que protegem contra conexao lenta.
func montarServidor(endereco string, rotas http.Handler) *http.Server {
	return &http.Server{
		Addr:              endereco,
		Handler:           rotas,
		ReadHeaderTimeout: tempoLimiteDeLeituraDoCabecalho,
	}
}

func servir(servidor *http.Server, nome string, falhas chan<- error) {
	// Os certificados ja estao em TLSConfig, entao ListenAndServeTLS recebe caminhos
	// vazios de proposito — e o mesmo par, carregado uma vez, com a validacao de
	// cliente que ele carrega junto.
	servir := servidor.ListenAndServe
	if servidor.TLSConfig != nil {
		servir = func() error { return servidor.ListenAndServeTLS("", "") }
	}

	if err := servir(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		falhas <- fmt.Errorf("servidor de %s: %w", nome, err)
	}
}

// montarTLSDoIngresso liga mTLS no caminho de aquisicao, quando ha credencial.
//
// TRES ESTADOS, e o do meio e o que importa:
//
//	nenhum caminho informado  — TLS desligado; avisa alto e segue
//	os tres informados        — mTLS ligado, certificado de cliente EXIGIDO
//	alguns informados         — ERRO, e derruba a partida
//
// O terceiro caso e o que evita o desastre silencioso. Alguem que informa a CA e
// esquece a chave espera estar com autenticacao ligada; subir sem ela produziria um
// gateway que aceita qualquer origem, com o operador convencido do contrario. Um
// meio-termo aqui e pior que qualquer um dos extremos.
func montarTLSDoIngresso(material credencial.Material, registro *slog.Logger) (*tls.Config, error) {
	if !material.Algum() {
		registro.Warn("INGRESSO SEM AUTENTICACAO: qualquer origem alcancavel na rede pode " +
			"entregar dado, e a identidade que ela reivindica nao e verificada. " +
			"Use -ca, -certificado e -chave. So opere assim em rede controlada")
		return nil, nil
	}
	if !material.Completo() {
		return nil, fmt.Errorf(
			"credenciais incompletas: -ca, -certificado e -chave precisam vir juntos. " +
				"Subir com parte delas daria a impressao de autenticacao sem haver nenhuma")
	}

	configuracao, err := credencial.ConfiguracaoDeServidor(material)
	if err != nil {
		return nil, err
	}

	registro.Info("ingresso com mTLS: certificado de cliente exigido e identidade confrontada")
	return configuracao, nil
}

// desligar encerra os dois servidores esperando pelas requisicoes em curso.
//
// Contexto proprio porque o do processo ja foi cancelado. Sem ele, o desligamento
// cortaria uma remessa no meio da gravacao — e a origem, sem confirmacao,
// retransmitiria. Nada se perderia, mas o trabalho seria refeito a toa.
func desligar(ingresso, apresentacao *http.Server, registro *slog.Logger) {
	ctx, cancelar := context.WithTimeout(context.Background(), tempoLimiteDoDesligamento)
	defer cancelar()

	if err := ingresso.Shutdown(ctx); err != nil {
		registro.Warn("desligamento do ingresso excedeu o tempo limite", slog.String("erro", err.Error()))
	}
	if err := apresentacao.Shutdown(ctx); err != nil {
		registro.Warn("desligamento da apresentacao excedeu o tempo limite", slog.String("erro", err.Error()))
	}
	registro.Info("synkacore-gateway encerrado")
}

// podarPeriodicamente remove do diario o que ja foi projetado e envelheceu.
//
// Append-only sem poda cresce sem limite, e o disco de um equipamento que ninguem
// visita e finito. A poda so remove o que satisfaz as DUAS condicoes — ja projetado
// e mais antigo que a retencao —, e a garantia esta no proprio Podar.
func podarPeriodicamente(ctx context.Context, diario *diariosqlite.Diario,
	r relogio.Relogio, registro *slog.Logger) {

	temporizador := time.NewTicker(intervaloDePodaPadrao)
	defer temporizador.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-temporizador.C:
			removidos, err := diario.Podar(ctx, retencaoDoDiarioPadrao, r.Agora())
			if err != nil {
				registro.Error("poda do diario falhou", slog.String("erro", err.Error()))
				continue
			}
			if removidos > 0 {
				registro.Info("diario podado", slog.Int64("registros_removidos", removidos))
			}
		}
	}
}

// fracaoDeAlertaDaOcupacao e a partir de quanto do teto o gateway comeca a avisar.
//
// Oitenta por cento da folga para agir ANTES da recusa. Avisar so ao alcancar o teto
// seria avisar junto com o sintoma, e quem opera precisa da diferenca entre "vai
// encher" e "encheu" — a primeira e agendavel, a segunda e uma parada.
const fracaoDeAlertaDaOcupacao = 0.8

// vigiarOcupacaoDoDiario remede o tamanho do diario periodicamente.
//
// Laco proprio, e nao junto da poda, porque as cadencias sao de ordens diferentes: a
// poda roda de hora em hora e a ocupacao precisa ser conhecida em segundos. Amarrar
// as duas faria o gateway descobrir que passou do teto uma hora depois de ter passado.
//
// O aviso e emitido apenas na TRANSICAO para cada faixa, e nao a cada ciclo. Um log
// que repete a mesma linha a cada dez segundos durante dias enterra a informacao util
// e enche o disco — que e precisamente o recurso em disputa aqui.
func vigiarOcupacaoDoDiario(ctx context.Context, diario *diariosqlite.Diario,
	registro *slog.Logger) {

	temporizador := time.NewTicker(intervaloDeOcupacaoPadrao)
	defer temporizador.Stop()

	avisado, recusando := false, false

	for {
		select {
		case <-ctx.Done():
			return
		case <-temporizador.C:
			medido, err := diario.MedirOcupacao()
			if err != nil {
				registro.Warn("nao foi possivel medir a ocupacao do diario",
					slog.String("erro", err.Error()))
				continue
			}

			// Teto desligado: nao ha faixa a reportar, e avisar sobre um limite que
			// nao existe treinaria quem le a ignorar o aviso quando ele existir.
			_, teto := diario.Ocupacao()
			if teto <= 0 {
				continue
			}

			switch {
			case medido >= teto && !recusando:
				registro.Error("DIARIO NO TETO: o gateway esta RECUSANDO remessa para nao encher "+
					"o disco. As origens estao bufferizando e vao retransmitir. Ligue a projecao "+
					"para que a poda libere espaco, aumente -diario-maximo, ou libere disco",
					slog.Int64("ocupado_bytes", medido),
					slog.Int64("teto_bytes", teto))
				recusando, avisado = true, true

			case medido < teto && recusando:
				registro.Info("diario voltou para baixo do teto: as remessas voltaram a ser aceitas",
					slog.Int64("ocupado_bytes", medido),
					slog.Int64("teto_bytes", teto))
				recusando = false

			case !recusando && float64(medido) >= float64(teto)*fracaoDeAlertaDaOcupacao && !avisado:
				registro.Warn("diario passou de 80% do teto; sem projecao configurada ele NAO e podado",
					slog.Int64("ocupado_bytes", medido),
					slog.Int64("teto_bytes", teto))
				avisado = true

			case float64(medido) < float64(teto)*fracaoDeAlertaDaOcupacao:
				avisado = false
			}
		}
	}
}

// montarRegistro configura o log estruturado.
//
// log/slog da biblioteca padrao, direto, sem envolver num logger proprio: envolver
// so produziria uma API pior e um lugar a mais para a configuracao divergir.
func montarRegistro(nivel string) *slog.Logger {
	var severidade slog.Level
	if err := severidade.UnmarshalText([]byte(nivel)); err != nil {
		severidade = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: severidade}))
}
