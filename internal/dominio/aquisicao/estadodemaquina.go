package aquisicao

import (
	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const operacaoEstadoDeMaquina = "aquisicao.estadoDeMaquinaDe"

// EstadoDeMaquina declara o estado OBSERVADO de uma maquina.
//
// A decisao mais importante deste tipo: ele NAO declara como o estado afeta a
// disponibilidade. Se setup desconta do tempo programado e regra de negocio, vive
// no gateway e e configuravel por planta — porque plantas discordam disso,
// legitimamente. Codificar a politica aqui obrigaria mudar o contrato para
// atender um cliente, que e a coisa mais cara de mudar num produto replicado.
//
// A origem observa. O gateway decide o que significa.
type EstadoDeMaquina uint8

const (
	// EstadoRodando: produzindo.
	EstadoRodando EstadoDeMaquina = iota + 1

	// EstadoParada: parada AINDA NAO CLASSIFICADA.
	//
	// Existe separado porque e legitimo: o operador pode nao ter classificado, e
	// forcar uma classificacao errada e pior que admitir a falta.
	EstadoParada

	// EstadoSetup: troca de ferramenta ou preparacao. Parada planejada.
	EstadoSetup

	// EstadoOciosa: maquina saudavel, parada por falta de insumo ou de operador.
	//
	// Separado de quebra porque e problema de LOGISTICA, nao de manutencao.
	// Juntos, o indicador culpa a maquina errada.
	EstadoOciosa

	// EstadoManutencaoProgramada: preventiva planejada.
	//
	// Separado de quebra porque, somados, toda preventiva bem-feita PIORA o
	// indicador — e ai ninguem quer fazer preventiva. Um indicador que pune a
	// pratica correta acaba mudando a pratica, nao o indicador.
	EstadoManutencaoProgramada

	// EstadoQuebra: falha nao planejada. Base do tempo medio entre falhas, e
	// portanto de toda analise preditiva futura.
	EstadoQuebra
)

// String devolve o nome estavel do estado, usado no modelo de leitura e em
// metrica. Ingles por convencao de saida; ver falha.Categoria.String.
func (e EstadoDeMaquina) String() string {
	switch e {
	case EstadoRodando:
		return "running"
	case EstadoParada:
		return "stopped"
	case EstadoSetup:
		return "setup"
	case EstadoOciosa:
		return "idle"
	case EstadoManutencaoProgramada:
		return "planned_maintenance"
	case EstadoQuebra:
		return "breakdown"
	}
	return "unspecified"
}

// estadoDeMaquinaDe converte o estado do contrato para o tipo de dominio.
//
// Sem clausula default, para que o linter exhaustive cobre cada estado novo aqui
// no dia em que ele entrar no contrato. Um estado que caisse num default viraria
// silenciosamente outro estado, e o relatorio de disponibilidade ficaria errado
// sem nada acusar.
//
// NAO_ESPECIFICADO e recusado de proposito: e o valor zero do protobuf, ou seja,
// e o que chega quando a origem esqueceu de preencher o campo. Aceita-lo
// significaria registrar "a maquina esta em algum estado" como se fosse um fato.
func estadoDeMaquinaDe(doFio contratov1.EstadoDeMaquina) (EstadoDeMaquina, error) {
	switch doFio {
	case contratov1.EstadoDeMaquina_ESTADO_DE_MAQUINA_RODANDO:
		return EstadoRodando, nil
	case contratov1.EstadoDeMaquina_ESTADO_DE_MAQUINA_PARADA:
		return EstadoParada, nil
	case contratov1.EstadoDeMaquina_ESTADO_DE_MAQUINA_SETUP:
		return EstadoSetup, nil
	case contratov1.EstadoDeMaquina_ESTADO_DE_MAQUINA_OCIOSA:
		return EstadoOciosa, nil
	case contratov1.EstadoDeMaquina_ESTADO_DE_MAQUINA_MANUTENCAO_PROGRAMADA:
		return EstadoManutencaoProgramada, nil
	case contratov1.EstadoDeMaquina_ESTADO_DE_MAQUINA_QUEBRA:
		return EstadoQuebra, nil
	case contratov1.EstadoDeMaquina_ESTADO_DE_MAQUINA_NAO_ESPECIFICADO:
		return 0, falha.Nova(falha.CategoriaEntradaInvalida, operacaoEstadoDeMaquina,
			"mudanca de estado de maquina sem estado declarado")
	}
	return 0, falha.Nova(falha.CategoriaEntradaInvalida, operacaoEstadoDeMaquina,
		"estado de maquina desconhecido por este gateway")
}
