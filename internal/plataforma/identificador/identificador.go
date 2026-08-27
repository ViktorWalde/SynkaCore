// Package identificador gera os identificadores sorteados do sistema.
//
// Existe como package proprio, e nao como funcao solta em cada lugar que precisa
// de um, porque o alfabeto precisa ser o MESMO em todos eles: os identificadores
// atravessam o contrato de fio e sao validados por identidadededispositivo, cujo
// padrao aceita apenas minusculas, digitos e hifen. Um gerador que produzisse
// maiuscula ou sublinhado criaria uma origem que o gateway recusa — e a falha
// apareceria na primeira remessa, em campo.
package identificador

import (
	"crypto/rand"
	"encoding/hex"
)

// bytesDeEntropia define o tamanho do identificador sorteado.
//
// Oito bytes dao 16 digitos hexadecimais e 2^64 valores possiveis. Para sessao de
// boot isso e folgado: a colisao so importaria entre duas partidas do MESMO
// dispositivo, e a probabilidade de repetir e desprezivel mesmo com reinicios
// diarios por decadas.
const bytesDeEntropia = 8

// Sortear devolve um identificador aleatorio no alfabeto aceito pelo contrato.
//
// Usa crypto/rand, e nao math/rand, porque o identificador de sessao de boot e
// componente da chave de idempotencia: um gerador previsivel permitiria a alguem
// forjar chaves que colidem com as de uma origem legitima, e o dado real seria
// descartado como duplicata.
//
// A falha e panico de proposito. Nao ha o que fazer sem entropia — uma sessao de
// boot previsivel comprometeria a deduplicacao —, e falhar na partida e melhor que
// operar com identidade fraca.
func Sortear(prefixo string) string {
	bruto := make([]byte, bytesDeEntropia)
	if _, err := rand.Read(bruto); err != nil {
		panic("identificador: entropia indisponivel: " + err.Error())
	}
	return prefixo + hex.EncodeToString(bruto)
}
