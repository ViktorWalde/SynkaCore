package instalacao

import (
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const operacaoNovaAdmissao = "instalacao.NovaAdmissao"

// Admissao declara quanto cada classe de dado tolera esperar NA PORTA do gateway.
//
// O QUE ESTES NUMEROS SAO, e a distincao decide o desenho inteiro: eles sao uma
// PROMESSA SOBRE O DADO, e nao um parametro de capacidade da maquina.
//
// "Uma amostra nao espera mais que dois segundos" e uma afirmacao da mesma especie
// que ClasseDeDado.LatenciaMaximaDeEntrega, que ja diz ha quanto tempo uma amostra
// pode envelhecer no buffer da origem. Por isso eles moram no dominio e viajam pela
// configuracao da instalacao, e nao por bandeira de linha de comando.
//
// A CONSEQUENCIA QUE PRECISA SER DITA EM VOZ ALTA: num disco mais lento, o gateway
// passa a recusar com a fila mais rasa. Isso NAO e um defeito a compensar. A
// promessa continua sendo cumprida, e o que a planta descobre e que aquele hardware
// nao comporta aquela frota — que e informacao verdadeira e acionavel.
//
// A alternativa recusada era derivar o orcamento do custo medido do disco, para que
// a mesma frota coubesse em qualquer maquina. Ela apaga a promessa: uma amostra
// passaria a esperar trinta segundos num disco ruim sem que ninguem tivesse
// declarado isso, e o sistema se acomodaria em silencio ao pior hardware. Um limite
// que se ajusta sozinho ao que o encosta deixou de ser um limite.
//
// O que o disco de fato determina — o custo de uma gravacao — e MEDIDO, e entra
// pelo outro lado: a portaria estima a espera multiplicando esse custo pela fila.
// Medicao e politica ficam separadas de propósito.
type Admissao struct {
	// OrcamentoDaAmostra e quanto uma remessa so de amostras aceita esperar antes
	// de o gateway preferir recusa-la.
	OrcamentoDaAmostra time.Duration

	// OrcamentoDoEventoDiscreto e quanto uma remessa que carrega evento discreto
	// aceita esperar.
	//
	// MAIOR que o da amostra, sempre, e a diferenca entre os dois E a reserva.
	// Amostra prefere ser recusada a esperar, porque a proxima repoe quase a mesma
	// informacao; evento discreto prefere esperar, porque nao existe proximo que o
	// reponha.
	OrcamentoDoEventoDiscreto time.Duration

	// FilaMaxima limita quantas remessas podem aguardar admissao, e existe por
	// MEMORIA — nao por politica.
	//
	// Cada uma que espera e uma goroutine com o corpo da remessa vivo na mao. Sem
	// teto, uma saturacao prolongada troca "resposta lenta" por "gateway morto por
	// falta de memoria", que e o unico modo de falha que a aquisicao nao sobrevive.
	//
	// Esta na configuracao da planta porque e a planta que sabe quantas origens
	// existem e quanta RAM o equipamento tem.
	FilaMaxima int
}

// Padroes da admissao, dimensionados a partir da medicao da V2.3.
//
// Eles nao foram escolhidos para parecer prudentes. Naquela medicao, 200 origens
// entregando 100 envelopes cada custaram ~33 us por envelope, ou ~3,3 ms por
// remessa: a fila precisaria passar de seiscentas esperando para a estimativa
// alcancar dois segundos, e com 200 origens ela nao passa de 200. Em 400 origens,
// onde a mesma medicao registrou p99 de 5,6 s, ela passa.
//
// Ou seja: a recusa comeca onde a medicao mostrou que o sistema para de dar conta,
// e nao antes. A rodada de verificacao da V2.4 confirmou — 200 origens em vinte
// segundos produziram UMA recusa.
const (
	orcamentoDaAmostraPadrao        = 2 * time.Second
	orcamentoDoEventoDiscretoPadrao = 10 * time.Second

	// filaMaximaPadrao e folgado para nunca ser o limite que morde primeiro: quem
	// decide a recusa no uso normal e o orcamento de espera. Nas rodadas da V2.4,
	// com 400 origens, a fila estabilizou em ~390.
	filaMaximaPadrao = 1024
)

// AdmissaoPadrao devolve a politica de admissao usada quando a instalacao nao
// declara nenhuma.
//
// Esta e a FONTE AUTORITATIVA destes numeros. O package de contrapressao tem
// padroes proprios para poder ser exercitado sozinho, e um teste no adaptador de
// ingresso — que e quem traduz um vocabulario no outro — exige que os dois
// concordem. Dois conjuntos do mesmo valor divergem, e o que ninguem lembra de
// atualizar e sempre o que esta mais longe do arquivo de configuracao.
func AdmissaoPadrao() Admissao {
	return Admissao{
		OrcamentoDaAmostra:        orcamentoDaAmostraPadrao,
		OrcamentoDoEventoDiscreto: orcamentoDoEventoDiscretoPadrao,
		FilaMaxima:                filaMaximaPadrao,
	}
}

// NovaAdmissao valida uma politica declarada pela instalacao.
//
// Como toda validacao de configuracao deste projeto, ela roda na PARTIDA: um
// orcamento incoerente derruba o gateway com mensagem clara em vez de virar recusa
// errada em silencio meses depois.
func NovaAdmissao(politica Admissao) (Admissao, error) {
	if politica.OrcamentoDaAmostra <= 0 {
		return Admissao{}, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaAdmissao,
			"admissao: espera_maxima_da_amostra precisa ser positiva")
	}
	if politica.OrcamentoDoEventoDiscreto <= 0 {
		return Admissao{}, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaAdmissao,
			"admissao: espera_maxima_do_evento precisa ser positiva")
	}

	// A TRAVA QUE IMPORTA. Com o orcamento do evento MENOR que o da amostra, a
	// reserva se inverte: o gateway passaria a recusar parada de maquina antes de
	// recusar leitura de temperatura, trocando a garantia mais forte do sistema pela
	// mais fraca.
	//
	// E o pior e que nada denunciaria isso. O gateway continuaria funcionando,
	// aceitando dado, respondendo saudavel — e a contagem de paradas ficaria
	// permanentemente errada, exatamente o buraco silencioso que a ClasseDeDado
	// existe para tornar impossivel. Por isso e erro de partida, e nao aviso.
	if politica.OrcamentoDoEventoDiscreto < politica.OrcamentoDaAmostra {
		return Admissao{}, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaAdmissao,
			"admissao: espera_maxima_do_evento ("+politica.OrcamentoDoEventoDiscreto.String()+
				") e menor que espera_maxima_da_amostra ("+politica.OrcamentoDaAmostra.String()+
				"). Isso inverteria a reserva: o gateway recusaria evento discreto antes de "+
				"recusar amostra, e a perda seria silenciosa")
	}

	if politica.FilaMaxima < 1 {
		return Admissao{}, falha.Nova(falha.CategoriaEntradaInvalida, operacaoNovaAdmissao,
			"admissao: fila_maxima precisa ser ao menos 1")
	}

	return politica, nil
}

// Admissao devolve a politica de admissao desta instalacao.
func (i *Instalacao) Admissao() Admissao { return i.admissao }
