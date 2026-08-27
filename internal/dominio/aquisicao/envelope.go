package aquisicao

import (
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const (
	// TamanhoMaximoDoConteudoEmBytes limita o conteudo de uma unica mensagem.
	//
	// Duplo proposito: cabe no orcamento de memoria de uma origem embarcada e
	// fecha o vetor de exaustao de memoria do gateway por mensagem gigante — uma
	// origem comprometida nao derruba a planta enviando megabytes.
	TamanhoMaximoDoConteudoEmBytes = 8 * 1024

	// VersaoMinimaDoEsquema e a versao de envelope mais antiga ainda aceita.
	//
	// O gateway aceita TODAS as versoes ja publicadas, para sempre: nao existe
	// janela de manutencao que alcance 100% da frota, e a origem que falha a
	// atualizacao nao pode virar dado faltando em silencio num relatorio. Este
	// valor portanto nunca avanca.
	VersaoMinimaDoEsquema uint16 = 1

	// VersaoMaximaDoEsquema e a versao de envelope mais recente reconhecida.
	VersaoMaximaDoEsquema uint16 = 1

	// TempoLigadoMaximoPlausivel recusa tempo ligado absurdo, sintoma de quadro
	// corrompido ou origem defeituosa. Cem anos e folgado o bastante para nunca
	// recusar dado legitimo, e apertado o bastante para pegar lixo.
	TempoLigadoMaximoPlausivel = 100 * 365 * 24 * time.Hour

	// tempoLigadoMaximoEmMilissegundos e TempoLigadoMaximoPlausivel na unidade que
	// chega do fio.
	//
	// A comparacao acontece em uint64, ANTES de qualquer conversao para
	// time.Duration, e o motivo e um defeito real: converter um tempo ligado
	// hostil como 1<<62 e multiplicar por time.Millisecond estoura o int64 de
	// time.Duration e envolve para um valor pequeno e plausivel — a validacao
	// passaria e o quadro corrompido entraria no sistema.
	//
	// A regra que fica: VALIDAR NA LARGURA EM QUE O DADO CHEGOU, antes de qualquer
	// conversao. Quem escolhe o numero e o adversario.
	tempoLigadoMaximoEmMilissegundos = uint64(TempoLigadoMaximoPlausivel / time.Millisecond)

	operacaoNovoEnvelope = "aquisicao.NovoEnvelope"
)

// ParametrosDeEnvelope e a lista de argumentos de NovoEnvelope, na forma bruta em
// que os codecs a extraem do fio.
//
// NAO e um modelo paralelo de dominio: nao tem comportamento, nao e armazenada e
// nao circula pelo sistema. E apenas a assinatura do construtor, que ficaria
// ilegivel com oito parametros posicionais — e, pior, sujeita a troca silenciosa
// de dois argumentos adjacentes do mesmo tipo. O unico tipo que representa uma
// mensagem no SynkaCore e Envelope.
type ParametrosDeEnvelope struct {
	VersaoDoEsquema   uint16
	IDDoDispositivo   string
	IDDaSessaoDeBoot  string
	NumeroDeSequencia uint64
	TempoLigadoMs     uint64
	Tipo              string
	Conteudo          []byte

	// InstanteObservado e carimbado pelo GATEWAY na recepcao, nunca pela origem.
	// Ver package autoridadedetempo.
	InstanteObservado time.Time
}

// Envelope e a representacao canonica de toda mensagem que entra no SynkaCore.
//
// Todo transporte e todo codec convergem para este tipo. Os codecs sao
// adaptadores de formato; a estrutura logica e as regras sao unicas.
//
// Campos nao exportados por decisao: NovoEnvelope e o unico caminho de
// construcao, entao POSSUIR UM ENVELOPE E PROVA DE QUE ELE E VALIDO. Nenhuma
// camada acima revalida, e nenhum adaptador consegue fabricar um invalido.
type Envelope struct {
	versaoDoEsquema      uint16
	chaveDeIdempotencia  ChaveDeIdempotencia
	tempoLigado          time.Duration
	classeDeDado         ClasseDeDado
	tipo                 TipoDeConteudo
	conteudoBruto        []byte
	conteudoDecodificado ConteudoDecodificado
	instanteObservado    time.Time
}

// NovoEnvelope valida os dados brutos de uma mensagem e constroi o Envelope canonico.
//
// Este e o UNICO ponto de validacao de mensagem do sistema. Handler HTTP, leitor
// de barramento e teste passam todos por aqui. A consequencia pratica do
// invariante de nao-duplicacao e esta: NAO EXISTE "validar de novo por seguranca"
// em camada alguma acima — essa e a origem mais comum de validacao divergente,
// duas checagens da mesma coisa que discordam depois de seis meses.
func NovoEnvelope(parametros ParametrosDeEnvelope, catalogo *CatalogoDeConteudo) (Envelope, error) {
	if catalogo == nil {
		return Envelope{}, falha.Nova(falha.CategoriaInterna,
			operacaoNovoEnvelope, "catalogo de conteudo nao fornecido")
	}
	if parametros.VersaoDoEsquema < VersaoMinimaDoEsquema ||
		parametros.VersaoDoEsquema > VersaoMaximaDoEsquema {
		return Envelope{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoNovoEnvelope, "versao de esquema do envelope fora da faixa suportada")
	}

	idDoDispositivo, err := identidadededispositivo.AnalisarIDDoDispositivo(parametros.IDDoDispositivo)
	if err != nil {
		return Envelope{}, err
	}
	idDaSessaoDeBoot, err := identidadededispositivo.AnalisarIDDaSessaoDeBoot(parametros.IDDaSessaoDeBoot)
	if err != nil {
		return Envelope{}, err
	}

	if parametros.TempoLigadoMs > tempoLigadoMaximoEmMilissegundos {
		return Envelope{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoNovoEnvelope, "tempo ligado implausivel: quadro provavelmente corrompido")
	}
	tempoLigado := time.Duration(parametros.TempoLigadoMs) * time.Millisecond

	if parametros.InstanteObservado.IsZero() {
		// Falha do gateway, nao da origem: quem carimba a recepcao somos nos.
		return Envelope{}, falha.Nova(falha.CategoriaInterna,
			operacaoNovoEnvelope, "instante de observacao nao carimbado pelo gateway")
	}

	if len(parametros.Conteudo) == 0 {
		return Envelope{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoNovoEnvelope, "conteudo vazio")
	}
	if len(parametros.Conteudo) > TamanhoMaximoDoConteudoEmBytes {
		return Envelope{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoNovoEnvelope, "conteudo excede o tamanho maximo permitido")
	}

	definicao, err := catalogo.Buscar(TipoDeConteudo(parametros.Tipo))
	if err != nil {
		return Envelope{}, err
	}

	// Decodifica UMA vez: valida a estrutura agora e serve a projecao depois, sem
	// uma segunda funcao de validacao que divergiria com o tempo.
	conteudoDecodificado, err := definicao.Decodificar(parametros.Conteudo)
	if err != nil {
		return Envelope{}, falha.Envolver(falha.CategoriaEntradaInvalida,
			operacaoNovoEnvelope, "conteudo invalido para o tipo "+parametros.Tipo, err)
	}

	// Copia defensiva: o buffer de origem pertence ao adaptador de transporte e
	// costuma ser reaproveitado entre mensagens. Sem a copia, o Envelope mudaria
	// de conteudo depois de construido.
	conteudoBruto := make([]byte, len(parametros.Conteudo))
	copy(conteudoBruto, parametros.Conteudo)

	return Envelope{
		versaoDoEsquema: parametros.VersaoDoEsquema,
		chaveDeIdempotencia: NovaChaveDeIdempotencia(
			idDoDispositivo, idDaSessaoDeBoot, parametros.NumeroDeSequencia),
		tempoLigado:          tempoLigado,
		classeDeDado:         definicao.Classe,
		tipo:                 definicao.Tipo,
		conteudoBruto:        conteudoBruto,
		conteudoDecodificado: conteudoDecodificado,
		instanteObservado:    parametros.InstanteObservado.UTC(),
	}, nil
}

// VersaoDoEsquema devolve a versao do envelope como recebida da origem.
//
// NAO e portao de compatibilidade — o gateway aceita todas as versoes ja
// publicadas. E observabilidade de frota: responde "quais origens ainda nao foram
// atualizadas?" sem ir a campo conferir.
func (e Envelope) VersaoDoEsquema() uint16 { return e.versaoDoEsquema }

// ChaveDeIdempotencia devolve a chave de deduplicacao da mensagem.
func (e Envelope) ChaveDeIdempotencia() ChaveDeIdempotencia { return e.chaveDeIdempotencia }

// IDDoDispositivo devolve o dispositivo de origem.
func (e Envelope) IDDoDispositivo() identidadededispositivo.IDDoDispositivo {
	return e.chaveDeIdempotencia.IDDoDispositivo()
}

// IDDaSessaoDeBoot devolve a sessao de boot de origem.
func (e Envelope) IDDaSessaoDeBoot() identidadededispositivo.IDDaSessaoDeBoot {
	return e.chaveDeIdempotencia.IDDaSessaoDeBoot()
}

// TempoLigado devolve o tempo monotonico desde o boot da origem, como ela o mediu.
//
// Este e o tempo BRUTO, e e o unico que a origem tem autoridade para afirmar.
// Ver package autoridadedetempo para derivar o instante real estimado.
func (e Envelope) TempoLigado() time.Duration { return e.tempoLigado }

// InstanteObservado devolve o instante em que o gateway recebeu a mensagem.
func (e Envelope) InstanteObservado() time.Time { return e.instanteObservado }

// ClasseDeDado devolve a classe, e com ela as cinco politicas exigidas.
func (e Envelope) ClasseDeDado() ClasseDeDado { return e.classeDeDado }

// Tipo devolve o tipo de conteudo.
func (e Envelope) Tipo() TipoDeConteudo { return e.tipo }

// ConteudoDecodificado devolve o conteudo ja interpretado.
func (e Envelope) ConteudoDecodificado() ConteudoDecodificado { return e.conteudoDecodificado }

// ConteudoBruto devolve uma copia dos bytes canonicos do conteudo.
//
// E esta forma — e nao a decodificada — que o diario de ingestao persiste. O dado
// bruto da origem NUNCA e substituido por uma interpretacao nossa: se a
// decodificacao estiver errada, o original ainda permite reprocessar.
func (e Envelope) ConteudoBruto() []byte {
	conteudoBruto := make([]byte, len(e.conteudoBruto))
	copy(conteudoBruto, e.conteudoBruto)
	return conteudoBruto
}
