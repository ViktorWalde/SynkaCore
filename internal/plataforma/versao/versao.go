// Package versao declara a identidade desta compilacao do SynkaCore.
//
// NO CODIGO-FONTE, e nao injetada por ldflags na hora do build. A escolha e
// deliberada e tem tres consequencias que importam a um produto instalado em planta:
//
//  1. Ela entra no MANIFESTO criptografico. `make manifesto` monta a lista com
//     `git ls-files`; um numero que so existe na linha de comando do build ficaria
//     de fora, e o artefato de registro nao diria qual versao foi registrada.
//  2. `go build` puro produz um binario correto. Numa planta sem internet, quem
//     compila e um tecnico com Go instalado e nada mais — um binario que so sabe
//     sua versao quando o Makefile foi usado se apresentaria como "desconhecida"
//     justamente no caso em que a informacao mais falta.
//  3. Mudar de versao vira um commit, e nao um argumento esquecido.
//
// O CUSTO ACEITO: publicar exige lembrar de incrementar aqui. E o mesmo custo de
// incrementar a versao do catalogo de motivos na configuracao da instalacao, e pela
// mesma razao — um numero que se atualiza sozinho nao afirma nada sobre intencao.
package versao

// Numero identifica esta versao do SynkaCore.
//
// Semantico em tres partes por convencao de leitura, embora o projeto versione por
// entrega (V2.0, V2.1, ...) e nao por compatibilidade de API: nao ha API publica a
// estabilizar. O que precisa ser comparavel em campo e "esta planta esta atras da
// outra?", e tres numeros respondem isso melhor que um rotulo.
const Numero = "2.7.0"

// Produto e o nome que sai no fio e no log.
const Produto = "synkacore"

// Completa devolve a identificacao usada em log, no health check e no descritor que
// a origem envia.
//
// Um formato so, num lugar so. Duas montagens do mesmo rotulo divergiriam, e o painel
// de comissionamento — que responde "quais origens ainda nao foram atualizadas?" —
// passaria a comparar textos que nao sao comparaveis.
func Completa() string { return Produto + "/" + Numero }
