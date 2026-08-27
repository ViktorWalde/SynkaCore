package aquisicao

import (
	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

// TipoSaudeDaOrigem e a telemetria interna de quem produz o dado.
const TipoSaudeDaOrigem TipoDeConteudo = "saude_da_origem"

const operacaoDecodificarSaude = "aquisicao.DecodificarSaudeDaOrigem"

// SaudeDaOrigem transforma uma promessa nao verificavel num sintoma observavel.
//
// "Imunidade a vazamento de memoria por escopo enxuto" e afirmacao falsa: quem
// aloca numa origem embarcada nao e a logica de negocio, e a infraestrutura —
// handshake TLS, serializacao e sobretudo o ciclo de reconexao de rede.
//
// E o risco real nem e o vazamento classico, e a FRAGMENTACAO: o total livre
// continua alto enquanto o maior bloco contiguo encolhe, ate a proxima alocacao de
// TLS falhar, tipicamente depois de dias ou semanas de operacao.
//
//	memoria livre alta + maior bloco baixo == fragmentacao avancada
//
// Por isso os dois campos viajam SEPARADOS. So o primeiro esconderia exatamente a
// falha que mata a origem em campo.
type SaudeDaOrigem struct {
	BytesLivresDeMemoria   uint32
	MaiorBlocoLivreEmBytes uint32

	// SinalDeRadioDbm ajuda a distinguir perda por rede de perda por defeito da
	// origem — duas causas com respostas operacionais completamente diferentes.
	SinalDeRadioDbm int32

	// ContagemDeReinicios denuncia reinicio em laco, que o watchdog mascara com
	// sucesso aparente: a origem responde, mas nunca fica de pe tempo suficiente
	// para servir.
	ContagemDeReinicios uint32

	// RegistrosDescartados e a contabilidade da politica de saturacao. Perda
	// aceita continua sendo perda CONHECIDA, em numeros.
	RegistrosDescartados uint32

	// BytesUsadosNoBuffer permite alarmar ANTES de saturar, e nao depois.
	BytesUsadosNoBuffer uint32
}

// Tipo implementa ConteudoDecodificado.
func (s SaudeDaOrigem) Tipo() TipoDeConteudo { return TipoSaudeDaOrigem }

// CamposProjetados implementa ConteudoDecodificado.
func (s SaudeDaOrigem) CamposProjetados() []CampoProjetado {
	return []CampoProjetado{
		{Nome: "heap_free_bytes", Valor: ValorNumerico(s.BytesLivresDeMemoria)},
		{Nome: "heap_largest_free_block_bytes", Valor: ValorNumerico(s.MaiorBlocoLivreEmBytes)},
		{Nome: "radio_signal_dbm", Valor: ValorNumerico(s.SinalDeRadioDbm)},
		{Nome: "reboot_count", Valor: ValorNumerico(s.ContagemDeReinicios)},
		{Nome: "discarded_record_count", Valor: ValorNumerico(s.RegistrosDescartados)},
		{Nome: "buffer_used_bytes", Valor: ValorNumerico(s.BytesUsadosNoBuffer)},
	}
}

// DefinicaoDeSaudeDaOrigem devolve a definicao de catalogo deste tipo.
func DefinicaoDeSaudeDaOrigem() DefinicaoDeConteudo {
	return definirConteudo(TipoSaudeDaOrigem, ClasseAmostra,
		"Saude interna da origem: memoria, fragmentacao, radio, reinicios e buffer.",
		func() *contratov1.SaudeDaOrigem { return &contratov1.SaudeDaOrigem{} },
		func(doFio *contratov1.SaudeDaOrigem) (ConteudoDecodificado, error) {
			saude := SaudeDaOrigem{
				BytesLivresDeMemoria:   doFio.GetBytesLivresDeMemoria(),
				MaiorBlocoLivreEmBytes: doFio.GetMaiorBlocoLivreEmBytes(),
				SinalDeRadioDbm:        doFio.GetSinalDeRadioDbm(),
				ContagemDeReinicios:    doFio.GetContagemDeReinicios(),
				RegistrosDescartados:   doFio.GetRegistrosDescartados(),
				BytesUsadosNoBuffer:    doFio.GetBytesUsadosNoBuffer(),
			}

			// O maior bloco livre nao pode exceder o total livre. Se exceder, a
			// leitura e incoerente — e como e justamente a relacao entre os dois
			// que denuncia fragmentacao, aceitar o par invalido inutilizaria o
			// unico sinal que este conteudo existe para dar.
			if saude.MaiorBlocoLivreEmBytes > saude.BytesLivresDeMemoria {
				return nil, falha.Nova(falha.CategoriaEntradaInvalida,
					operacaoDecodificarSaude,
					"maior bloco livre maior que a memoria livre total: leitura incoerente")
			}
			return saude, nil
		})
}
