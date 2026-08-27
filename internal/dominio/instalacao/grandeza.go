// Package instalacao modela a configuracao de uma planta: o que cada canal de
// cada dispositivo mede, e qual o vocabulario de motivos de parada dali.
//
// Este package existe para resolver a lacuna entre o que a origem AFIRMA e o que
// o dado SIGNIFICA. A origem reporta "canal 0 = 24,7". Sozinho, esse numero nao
// responde nada: nao se sabe se e temperatura ou pressao, em que unidade, nem de
// qual equipamento. A origem continua burra de proposito; e o gateway que deriva.
//
// A configuracao e AUTORITATIVA. O descritor que a origem envia declara o que ela
// ACREDITA medir, e serve para detectar discordancia entre os dois — nunca para
// substituir esta configuracao.
package instalacao

import (
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const (
	operacaoAnalisarGrandeza = "instalacao.AnalisarGrandeza"

	// prefixoDaGrandeza e o prefixo que o protobuf exige nos membros de enum, e
	// que a configuracao nao deve carregar.
	//
	// Escrever "GRANDEZA_TEMPERATURA" num arquivo editado por um tecnico em campo
	// seria atrito gratuito; ele escreve "temperatura".
	prefixoDaGrandeza = "GRANDEZA_"

	// nomeDaGrandezaNaoEspecificada e o membro zero do enum, recusado na
	// configuracao: um ponto de medicao sem grandeza declarada nao mede nada.
	nomeDaGrandezaNaoEspecificada = "NAO_ESPECIFICADO"
)

// Grandeza e o que um ponto de medicao mede.
//
// O tipo do CONTRATO e reusado diretamente, em vez de um enum proprio deste
// package. Um enum proprio seria uma segunda lista das mesmas grandezas, e duas
// listas do mesmo conjunto divergem — o gateway acabaria reconhecendo uma grandeza
// que o contrato nao transporta, ou o contrario.
type Grandeza = contratov1.Grandeza

// grandezasPorNome mapeia o nome usado na configuracao para o membro do enum.
//
// Construido por REFLEXAO sobre o descritor do protobuf, na inicializacao do
// package, e nunca escrito a mao. Acrescentar uma grandeza ao contrato a torna
// automaticamente utilizavel na configuracao, sem que ninguem precise lembrar de
// atualizar um mapa aqui — que e exatamente o tipo de esquecimento que produz "o
// gateway nao reconhece a grandeza que o contrato declara".
var grandezasPorNome = montarGrandezasPorNome()

func montarGrandezasPorNome() map[string]Grandeza {
	valores := contratov1.Grandeza(0).Descriptor().Values()
	porNome := make(map[string]Grandeza, valores.Len())

	for indice := range valores.Len() {
		valor := valores.Get(indice)
		nome := strings.TrimPrefix(string(valor.Name()), prefixoDaGrandeza)
		if nome == nomeDaGrandezaNaoEspecificada {
			continue
		}
		porNome[strings.ToLower(nome)] = Grandeza(valor.Number())
	}
	return porNome
}

// AnalisarGrandeza resolve o nome usado na configuracao para a grandeza do contrato.
//
// A mensagem de erro lista as grandezas aceitas em vez de apenas recusar. Quem le
// esse erro e um tecnico em campo comissionando um painel, e "grandeza
// desconhecida" sem a lista o obriga a ir procurar o contrato — que ele nao tem.
func AnalisarGrandeza(nome string) (Grandeza, error) {
	normalizado := strings.ToLower(strings.TrimSpace(nome))
	if normalizado == "" {
		return 0, falha.Nova(falha.CategoriaEntradaInvalida, operacaoAnalisarGrandeza,
			"ponto de medicao sem grandeza declarada")
	}

	grandeza, reconhecida := grandezasPorNome[normalizado]
	if !reconhecida {
		return 0, falha.Nova(falha.CategoriaEntradaInvalida, operacaoAnalisarGrandeza,
			"grandeza desconhecida: "+nome+". Aceitas: "+strings.Join(GrandezasAceitas(), ", "))
	}
	return grandeza, nil
}

// GrandezasAceitas devolve os nomes utilizaveis na configuracao, em ordem estavel.
//
// Ordem por NUMERO do enum, e nao alfabetica, porque ela agrupa as grandezas por
// familia — as de processo, as de qualidade de produto na agroindustria, as
// mecanicas, as eletricas — que e como quem configura pensa nelas.
func GrandezasAceitas() []string {
	valores := contratov1.Grandeza(0).Descriptor().Values()
	nomes := make([]string, 0, valores.Len())

	for indice := range valores.Len() {
		valor := valores.Get(indice)
		nome := strings.TrimPrefix(string(valor.Name()), prefixoDaGrandeza)
		if nome == nomeDaGrandezaNaoEspecificada {
			continue
		}
		nomes = append(nomes, strings.ToLower(nome))
	}
	return nomes
}

// NomeDaGrandeza devolve o nome estavel de uma grandeza, para o modelo de leitura.
//
// Devolve o nome do CONTRATO em minusculas, que e o mesmo aceito na configuracao —
// assim quem escreveu "temperatura" no arquivo encontra "temperatura" na coluna do
// banco, sem tradução no meio.
func NomeDaGrandeza(grandeza Grandeza) string {
	valor := contratov1.Grandeza(0).Descriptor().Values().ByNumber(protoreflect.EnumNumber(grandeza))
	if valor == nil {
		return "desconhecida"
	}
	return strings.ToLower(strings.TrimPrefix(string(valor.Name()), prefixoDaGrandeza))
}
