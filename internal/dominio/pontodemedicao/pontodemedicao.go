// Package pontodemedicao modela a identidade LOGICA de um ponto de medicao — a
// posicao na planta que e observada, independente de qual peca de hardware a
// observa hoje.
//
// A serie historica pertence ao PONTO, nao ao dispositivo. Substituir um
// dispositivo queimado e uma troca de vinculo (ver Vinculo), e o historico do
// ponto permanece continuo e comparavel ao longo dos anos.
package pontodemedicao

import (
	"regexp"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const (
	tamanhoMaximoDoID = 128

	operacaoAnalisarID = "pontodemedicao.AnalisarIDDoPontoDeMedicao"
	operacaoVinculo    = "pontodemedicao.NovoVinculo"
)

// padraoDeID permite hierarquia por ponto, refletindo a estrutura fisica da
// planta (por exemplo, "linha-2.prensa-01.temperatura-mancal").
var padraoDeID = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// IDDoPontoDeMedicao identifica unicamente um ponto de medicao da planta.
type IDDoPontoDeMedicao struct {
	valor string
}

// AnalisarIDDoPontoDeMedicao valida e constroi um IDDoPontoDeMedicao.
func AnalisarIDDoPontoDeMedicao(bruto string) (IDDoPontoDeMedicao, error) {
	if bruto == "" {
		return IDDoPontoDeMedicao{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoAnalisarID, "identificador de ponto de medicao vazio")
	}
	if len(bruto) > tamanhoMaximoDoID {
		return IDDoPontoDeMedicao{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoAnalisarID, "identificador de ponto de medicao excede o comprimento maximo")
	}
	if !padraoDeID.MatchString(bruto) {
		return IDDoPontoDeMedicao{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoAnalisarID, "identificador de ponto de medicao fora do alfabeto permitido")
	}
	return IDDoPontoDeMedicao{valor: bruto}, nil
}

// String devolve a forma textual canonica.
func (p IDDoPontoDeMedicao) String() string { return p.valor }

// Vazio informa se o IDDoPontoDeMedicao nao foi construido.
func (p IDDoPontoDeMedicao) Vazio() bool { return p.valor == "" }

// Vinculo e a ligacao temporal entre uma peca de hardware e um ponto de medicao.
//
// Modelado como intervalo fechado-aberto [VigenteDe, VigenteAte) para que a troca
// de hardware seja um fato datado e auditavel, e nao uma sobrescrita destrutiva.
// Perguntar "qual dispositivo alimentava este ponto em tal instante" continua
// respondivel anos depois.
type Vinculo struct {
	idDoPonto       IDDoPontoDeMedicao
	idDoDispositivo identidadededispositivo.IDDoDispositivo
	vigenteDe       time.Time
	vigenteAte      time.Time // zero == vinculo ainda aberto
}

// NovoVinculo constroi um vinculo aberto entre dispositivo e ponto de medicao.
func NovoVinculo(
	idDoPonto IDDoPontoDeMedicao,
	idDoDispositivo identidadededispositivo.IDDoDispositivo,
	vigenteDe time.Time,
) (Vinculo, error) {
	if idDoPonto.Vazio() {
		return Vinculo{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoVinculo, "vinculo exige ponto de medicao")
	}
	if idDoDispositivo.Vazio() {
		return Vinculo{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoVinculo, "vinculo exige dispositivo")
	}
	if vigenteDe.IsZero() {
		return Vinculo{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoVinculo, "vinculo exige instante inicial")
	}
	return Vinculo{
		idDoPonto:       idDoPonto,
		idDoDispositivo: idDoDispositivo,
		vigenteDe:       vigenteDe.UTC(),
	}, nil
}

// IDDoPontoDeMedicao devolve o ponto de medicao vinculado.
func (v Vinculo) IDDoPontoDeMedicao() IDDoPontoDeMedicao { return v.idDoPonto }

// IDDoDispositivo devolve o dispositivo vinculado.
func (v Vinculo) IDDoDispositivo() identidadededispositivo.IDDoDispositivo {
	return v.idDoDispositivo
}

// VigenteDe devolve o inicio da vigencia do vinculo.
func (v Vinculo) VigenteDe() time.Time { return v.vigenteDe }

// VigenteAte devolve o fim da vigencia, ou o tempo zero se ainda aberto.
func (v Vinculo) VigenteAte() time.Time { return v.vigenteAte }

// Aberto informa se o vinculo ainda vigora.
func (v Vinculo) Aberto() bool { return v.vigenteAte.IsZero() }

// EncerradoEm devolve uma copia do vinculo encerrada no instante indicado.
//
// Devolve copia em vez de mutar para que Vinculo permaneca imutavel: um vinculo
// ja persistido nunca muda sob os pes de quem o leu.
func (v Vinculo) EncerradoEm(instante time.Time) (Vinculo, error) {
	if !v.Aberto() {
		return Vinculo{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoVinculo, "vinculo ja encerrado")
	}
	if instante.Before(v.vigenteDe) {
		return Vinculo{}, falha.Nova(falha.CategoriaEntradaInvalida,
			operacaoVinculo, "encerramento anterior ao inicio da vigencia")
	}
	v.vigenteAte = instante.UTC()
	return v, nil
}

// CobreInstante informa se o vinculo vigorava no instante indicado.
func (v Vinculo) CobreInstante(instante time.Time) bool {
	if instante.Before(v.vigenteDe) {
		return false
	}
	return v.Aberto() || instante.Before(v.vigenteAte)
}
