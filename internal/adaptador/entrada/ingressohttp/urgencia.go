package ingressohttp

import (
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/instalacao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/contrapressao"
)

// urgenciaDe traduz a classe de dado do dominio para a urgencia da portaria.
//
// Este e o UNICO tradutor entre os dois vocabularios, pela mesma regra que faz
// statusDe ser o unico mapeador de falha.Categoria para status HTTP: a portaria e
// plataforma e nao pode conhecer ClasseDeDado, o dominio nao pode conhecer
// contrapressao, e quem os liga e o adaptador — num lugar so.
//
// A traducao inteira do sistema esta nestas duas linhas, e elas dizem a mesma
// coisa que as cinco politicas da classe ja diziam, aplicada agora a porta de
// entrada do gateway:
//
//	amostra         — tolera perda, logo tolera recusa. Recusar e barato, porque a
//	                  proxima amostra repoe quase a mesma informacao.
//	evento discreto — nao tem vizinho que o substitua. Recusar e caro, e por isso
//	                  ele aceita esperar mais antes de levar um nao.
//
// Sem clausula default: acrescentar uma ClasseDeDado precisa reprovar o build aqui
// tambem. Uma classe nova que caisse no padrao herdaria uma politica de admissao
// que ninguem escolheu para ela.
func urgenciaDe(classe aquisicao.ClasseDeDado) contrapressao.Urgencia {
	switch classe {
	case aquisicao.ClasseAmostra:
		return contrapressao.UrgenciaComum
	case aquisicao.ClasseEventoDiscreto:
		return contrapressao.UrgenciaReservada
	}
	// Classe invalida — que NovoEnvelope ja recusa — recebe a urgencia que espera
	// mais antes de ser recusada. Errar para o lado de preservar dado e sempre o
	// erro mais barato.
	return contrapressao.UrgenciaReservada
}

// urgenciaDaRemessa devolve a urgencia do lote inteiro.
//
// Um lote MISTO vale pelo item mais urgente, e nao pela maioria nem pela media. A
// razao e que a remessa e indivisivel: ela e admitida ou recusada por inteiro, e
// recusar um lote que carrega uma parada de maquina porque ele tambem carregava
// noventa e nove amostras trocaria a garantia mais forte do sistema pela mais
// fraca — exatamente a inversao que a ClasseDeDado existe para impedir.
//
// A consequencia aceita: uma origem cujo lote quase sempre carrega algum evento
// quase sempre alcanca o orcamento maior. Isso NAO e uma brecha explorável — a
// classe vem da anotacao do contrato, resolvida pelo gateway a partir do tipo de
// conteudo, e nao de nada que a remessa afirme. Para ser tratada como urgente, uma
// origem precisa de fato estar enviando eventos discretos.
func urgenciaDaRemessa(envelopes []aquisicao.Envelope) contrapressao.Urgencia {
	for _, envelope := range envelopes {
		if urgenciaDe(envelope.ClasseDeDado()) == contrapressao.UrgenciaReservada {
			return contrapressao.UrgenciaReservada
		}
	}
	return contrapressao.UrgenciaComum
}

// AjustesDaPortaria traduz a politica de admissao da instalacao para os ajustes da
// portaria.
//
// Segundo tradutor deste arquivo, e ele existe pela mesma razao do primeiro: a
// portaria e plataforma e nao pode conhecer instalacao, o dominio nao pode conhecer
// contrapressao, e quem os liga e o adaptador — num lugar so.
//
// O QUE NAO ATRAVESSA, e vale mais que o que atravessa:
//
//	VagasSimultaneas — fica em UM, sempre, e nao e configuravel. O diario tem um
//	                   escritor so (SetMaxOpenConns(1)); admitir mais nao produziria
//	                   paralelismo, e sim a mesma fila de volta dentro do
//	                   database/sql, onde ninguem a mede. Expor este numero
//	                   ofereceria a quem opera uma alavanca que so pode piorar.
//	EsperaMinima e   — sao o piso e o teto do Retry-After, e nao dizem respeito a
//	EsperaMaxima       planta. O piso existe porque o cabecalho tem resolucao de
//	                   segundos; o teto existe para que um gateway defeituoso nao
//	                   cale a frota. Nenhum dos dois melhora sendo ajustado por
//	                   instalacao.
func AjustesDaPortaria(admissao instalacao.Admissao) contrapressao.Ajustes {
	ajustes := contrapressao.AjustesPadrao()
	ajustes.OrcamentoComum = admissao.OrcamentoDaAmostra
	ajustes.OrcamentoReservado = admissao.OrcamentoDoEventoDiscreto
	ajustes.FilaMaxima = admissao.FilaMaxima
	return ajustes
}
