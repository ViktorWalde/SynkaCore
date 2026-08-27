package aquisicao

import (
	"strconv"
	"strings"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
)

// separadorDaChave divide os componentes na forma textual canonica. Nao pode
// ocorrer dentro de nenhum componente: o alfabeto dos identificadores
// (validado em identidadededispositivo) o exclui, e o numero de sequencia e
// decimal.
const separadorDaChave = ":"

// ChaveDeIdempotencia identifica unicamente uma mensagem produzida por uma
// origem, independente de quantas vezes ela seja entregue.
//
// Store-and-forward com retransmissao SEMPRE gera entrega duplicada — e
// consequencia do desenho, nao defeito. Sem chave de idempotencia, toda parada de
// maquina e contada duas vezes e todo relatorio fica errado, silenciosamente.
//
// A chave e NATURAL, nao sorteada, e cada componente e necessario:
//
//	idDoDispositivo   — separa origens distintas
//	idDaSessaoDeBoot  — impede colisao entre sessoes, ja que o contador de
//	                    sequencia reinicia em zero a cada partida
//	numeroDeSequencia — ordena e identifica dentro de uma sessao
//
// A deduplicacao acontece em UM lugar so: a restricao de unicidade sobre esta
// chave no diario de ingestao. Nenhuma camada acima refaz essa verificacao.
type ChaveDeIdempotencia struct {
	idDoDispositivo   identidadededispositivo.IDDoDispositivo
	idDaSessaoDeBoot  identidadededispositivo.IDDaSessaoDeBoot
	numeroDeSequencia uint64
}

// NovaChaveDeIdempotencia monta a chave a partir de identidades ja validadas.
//
// Nao revalida os identificadores: possuir um IDDoDispositivo ja e prova de que
// ele passou por AnalisarIDDoDispositivo. Revalidar aqui criaria a segunda
// checagem que diverge da primeira com o tempo.
func NovaChaveDeIdempotencia(
	idDoDispositivo identidadededispositivo.IDDoDispositivo,
	idDaSessaoDeBoot identidadededispositivo.IDDaSessaoDeBoot,
	numeroDeSequencia uint64,
) ChaveDeIdempotencia {
	return ChaveDeIdempotencia{
		idDoDispositivo:   idDoDispositivo,
		idDaSessaoDeBoot:  idDaSessaoDeBoot,
		numeroDeSequencia: numeroDeSequencia,
	}
}

// IDDoDispositivo devolve o dispositivo de origem.
func (c ChaveDeIdempotencia) IDDoDispositivo() identidadededispositivo.IDDoDispositivo {
	return c.idDoDispositivo
}

// IDDaSessaoDeBoot devolve a sessao de boot de origem.
func (c ChaveDeIdempotencia) IDDaSessaoDeBoot() identidadededispositivo.IDDaSessaoDeBoot {
	return c.idDaSessaoDeBoot
}

// NumeroDeSequencia devolve a posicao dentro da sessao de boot.
func (c ChaveDeIdempotencia) NumeroDeSequencia() uint64 { return c.numeroDeSequencia }

// String devolve a forma textual canonica da chave.
//
// Este e o UNICO formato de serializacao da chave no sistema. Indice de banco,
// log e rotulo de metrica usam esta funcao — nunca concatenam os componentes por
// conta propria. Dois formatos divergentes da mesma chave produzem falha de
// deduplicacao SILENCIOSA, que e a pior classe de defeito neste sistema: o
// relatorio conta duas vezes e nada acusa.
//
// Montada com strings.Builder em vez de fmt.Sprintf porque esta funcao roda uma
// vez por mensagem no caminho quente da ingestao, e Sprintf paga reflexao para
// formatar tres valores de tipo conhecido em tempo de compilacao.
func (c ChaveDeIdempotencia) String() string {
	dispositivo := c.idDoDispositivo.String()
	sessao := c.idDaSessaoDeBoot.String()

	var construtor strings.Builder
	construtor.Grow(len(dispositivo) + len(sessao) + 2*len(separadorDaChave) + 20)
	construtor.WriteString(dispositivo)
	construtor.WriteString(separadorDaChave)
	construtor.WriteString(sessao)
	construtor.WriteString(separadorDaChave)
	construtor.WriteString(strconv.FormatUint(c.numeroDeSequencia, 10))
	return construtor.String()
}
