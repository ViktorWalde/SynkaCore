// Package identidadededispositivo modela a identidade do HARDWARE — a peca fisica
// que observa, com sua credencial propria.
//
// Identidade de hardware e identidade de ponto de medicao sao coisas distintas e
// costumam ser misturadas. Trocar um dispositivo queimado NAO pode romper a serie
// historica, porque a serie pertence ao ponto de medicao (ver package
// pontodemedicao), nao a peca.
//
// Consequencia de seguranca: a credencial e POR DISPOSITIVO e nunca compartilhada.
// Comprometer um dispositivo de um armario destrancado compromete aquele
// dispositivo, nao a frota.
package identidadededispositivo

import (
	"regexp"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const (
	// tamanhoMaximoDoID limita o identificador para que ele caiba no envelope
	// binario do barramento serial, onde cada byte e recurso disputado.
	tamanhoMaximoDoID = 64

	operacaoAnalisarIDDoDispositivo  = "identidadededispositivo.AnalisarIDDoDispositivo"
	operacaoAnalisarIDDaSessaoDeBoot = "identidadededispositivo.AnalisarIDDaSessaoDeBoot"
)

// padraoDeID restringe o identificador a um alfabeto seguro para uso como chave
// de banco, rotulo de metrica e componente de caminho, sem escape.
var padraoDeID = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// IDDoDispositivo identifica unicamente uma peca de hardware.
//
// Unico na INSTALACAO inteira, nunca apenas dentro de um gateway: se dois
// gateways numerarem dispositivos a partir de 1, juntar os dados depois e
// irreversivel — e mudar espaco de nomes com dispositivos em campo e operacao de
// campo, nao refatoracao.
//
// O campo e nao exportado para que AnalisarIDDoDispositivo seja o unico caminho
// de construcao. Nenhum adaptador consegue fabricar um IDDoDispositivo invalido.
type IDDoDispositivo struct {
	valor string
}

// AnalisarIDDoDispositivo valida e constroi um IDDoDispositivo.
//
// Esta e a UNICA validacao de identificador de dispositivo do sistema. Nenhum
// handler, codec ou repositorio revalida — possuir um IDDoDispositivo e prova de
// que ele e valido.
func AnalisarIDDoDispositivo(bruto string) (IDDoDispositivo, error) {
	if err := validarIdentificador(bruto, operacaoAnalisarIDDoDispositivo, "dispositivo"); err != nil {
		return IDDoDispositivo{}, err
	}
	return IDDoDispositivo{valor: bruto}, nil
}

// String devolve a forma textual canonica.
func (d IDDoDispositivo) String() string { return d.valor }

// Vazio informa se o IDDoDispositivo nao foi construido.
func (d IDDoDispositivo) Vazio() bool { return d.valor == "" }

// IDDaSessaoDeBoot identifica uma partida especifica de um dispositivo.
//
// Razao de existir: uma origem sem relogio de tempo real com bateria comeca em
// 1970 ao ligar. Ela portanto NUNCA afirma saber a hora — reporta tempo
// monotonico desde o boot e sorteia um IDDaSessaoDeBoot a cada partida. E esse
// identificador que permite ao gateway ancorar aquele intervalo monotonico ao seu
// proprio relogio, e distinguir uma remessa antiga de uma nova apos queda de
// energia.
//
// Ele tambem e componente da chave de idempotencia: sem ele, os numeros de
// sequencia reiniciariam do zero a cada boot e colidiriam com dados anteriores.
type IDDaSessaoDeBoot struct {
	valor string
}

// AnalisarIDDaSessaoDeBoot valida e constroi um IDDaSessaoDeBoot.
func AnalisarIDDaSessaoDeBoot(bruto string) (IDDaSessaoDeBoot, error) {
	if err := validarIdentificador(bruto, operacaoAnalisarIDDaSessaoDeBoot, "sessao de boot"); err != nil {
		return IDDaSessaoDeBoot{}, err
	}
	return IDDaSessaoDeBoot{valor: bruto}, nil
}

// String devolve a forma textual canonica.
func (b IDDaSessaoDeBoot) String() string { return b.valor }

// Vazio informa se o IDDaSessaoDeBoot nao foi construido.
func (b IDDaSessaoDeBoot) Vazio() bool { return b.valor == "" }

// validarIdentificador concentra as tres checagens comuns aos identificadores
// deste package.
//
// Existe uma funcao, e nao uma copia por tipo, porque duas validacoes do mesmo
// conceito divergem com o tempo e passam a discordar sobre o que e um
// identificador aceitavel — precisamente o defeito que o invariante de
// nao-duplicacao existe para impedir.
func validarIdentificador(bruto, operacao, sujeito string) error {
	if bruto == "" {
		return falha.Nova(falha.CategoriaEntradaInvalida,
			operacao, "identificador de "+sujeito+" vazio")
	}
	if len(bruto) > tamanhoMaximoDoID {
		return falha.Nova(falha.CategoriaEntradaInvalida,
			operacao, "identificador de "+sujeito+" excede o comprimento maximo")
	}
	if !padraoDeID.MatchString(bruto) {
		return falha.Nova(falha.CategoriaEntradaInvalida,
			operacao, "identificador de "+sujeito+" fora do alfabeto permitido")
	}
	return nil
}
