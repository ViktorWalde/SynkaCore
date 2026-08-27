package aquisicao

import (
	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"

	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

// TipoMudancaDeEstadoDeMaquina registra a transicao de estado operacional.
const TipoMudancaDeEstadoDeMaquina TipoDeConteudo = "mudanca_de_estado_de_maquina"

const (
	operacaoDecodificarMudancaDeEstado = "aquisicao.DecodificarMudancaDeEstadoDeMaquina"

	// tamanhoMaximoDeIdentificadorAfirmado limita cracha e lote informados pelo
	// operador. Eles vem de leitura de cracha ou digitacao, nunca de catalogo, e
	// portanto sao entrada nao confiavel de tamanho arbitrario.
	tamanhoMaximoDeIdentificadorAfirmado = 64
)

// MudancaDeEstadoDeMaquina e o exemplo canonico de ClasseEventoDiscreto.
//
// Contraste com AmostraEscalar, que e ClasseAmostra: perder uma leitura de
// temperatura entre duas vizinhas nao altera conclusao nenhuma. Perder uma parada
// de maquina nao tem vizinha que a substitua — a contagem fica permanentemente
// errada e ninguem percebe. E por isso que as duas classes existem, e e por isso
// que a garantia de entrega e propriedade da classe e nao escolha do adaptador.
type MudancaDeEstadoDeMaquina struct {
	Endereco EnderecoDeCanal
	Estado   EstadoDeMaquina

	// CodigoDoMotivo e o motivo apontado na interface do operador, como codigo do
	// catalogo da instalacao. Zero significa parada AINDA NAO CLASSIFICADA, que e
	// informacao legitima e nao deve ser confundida com dado faltando.
	CodigoDoMotivo uint32

	// IDDoCrachaAfirmado e o que o leitor da origem capturou, bruto.
	//
	// A origem NAO valida o cracha: durante a autonomia de buffer nao ha a quem
	// perguntar, e guardar credenciais em cada origem multiplicaria a superficie
	// de ataque pela frota inteira. O gateway resolve depois, e guarda os dois
	// valores separados — cracha nao reconhecido vira anomalia auditavel, nao
	// registro perdido.
	//
	// DADO PESSOAL: identifica um trabalhador. Acesso restrito no modelo de leitura.
	IDDoCrachaAfirmado string

	// IDDoLoteAfirmado cobre o caso em que so o operador sabe qual lote esta
	// rodando. A associacao autoritativa e derivada no gateway, porque assim ela e
	// recomputavel se o registro do lote for corrigido.
	IDDoLoteAfirmado string
}

// EnderecoDoCanal implementa ConteudoEnderecado.
func (m MudancaDeEstadoDeMaquina) EnderecoDoCanal() EnderecoDeCanal { return m.Endereco }

// Tipo implementa ConteudoDecodificado.
func (m MudancaDeEstadoDeMaquina) Tipo() TipoDeConteudo { return TipoMudancaDeEstadoDeMaquina }

// CamposProjetados implementa ConteudoDecodificado.
//
// O cracha afirmado NAO e projetado aqui de proposito. Ele e dado pessoal, e o
// modelo de leitura e consultado por dashboard sem controle de acesso por coluna;
// expo-lo transformaria a trilha de auditoria em relatorio de vigilancia de
// trabalhador. Ele permanece no conteudo bruto do diario, acessivel a quem tem
// autorizacao para a auditoria.
func (m MudancaDeEstadoDeMaquina) CamposProjetados() []CampoProjetado {
	campos := append(camposDoEndereco(m.Endereco),
		CampoProjetado{Nome: "machine_state", Valor: ValorTexto(m.Estado.String())},
		CampoProjetado{Nome: "reason_code", Valor: ValorNumerico(m.CodigoDoMotivo)},
		CampoProjetado{Nome: "operator_acknowledged", Valor: ValorLogico(m.IDDoCrachaAfirmado != "")},
	)
	if m.IDDoLoteAfirmado != "" {
		campos = append(campos,
			CampoProjetado{Nome: "asserted_batch_id", Valor: ValorTexto(m.IDDoLoteAfirmado)})
	}
	return campos
}

// DefinicaoDeMudancaDeEstadoDeMaquina devolve a definicao de catalogo deste tipo.
func DefinicaoDeMudancaDeEstadoDeMaquina() DefinicaoDeConteudo {
	return definirConteudo(TipoMudancaDeEstadoDeMaquina, ClasseEventoDiscreto,
		"Transicao de estado operacional de uma maquina, com motivo e autoria afirmados.",
		func() *contratov1.MudancaDeEstadoDeMaquina { return &contratov1.MudancaDeEstadoDeMaquina{} },
		func(doFio *contratov1.MudancaDeEstadoDeMaquina) (ConteudoDecodificado, error) {
			estado, err := estadoDeMaquinaDe(doFio.GetEstado())
			if err != nil {
				return nil, err
			}
			mudanca := MudancaDeEstadoDeMaquina{
				Endereco:           enderecoDe(doFio.GetEndereco()),
				Estado:             estado,
				CodigoDoMotivo:     doFio.GetCodigoDoMotivo(),
				IDDoCrachaAfirmado: doFio.GetIdDoCrachaAfirmado(),
				IDDoLoteAfirmado:   doFio.GetIdDoLoteAfirmado(),
			}
			if len(mudanca.IDDoCrachaAfirmado) > tamanhoMaximoDeIdentificadorAfirmado {
				return nil, falha.Nova(falha.CategoriaEntradaInvalida,
					operacaoDecodificarMudancaDeEstado,
					"identificador de cracha afirmado excede o comprimento maximo")
			}
			if len(mudanca.IDDoLoteAfirmado) > tamanhoMaximoDeIdentificadorAfirmado {
				return nil, falha.Nova(falha.CategoriaEntradaInvalida,
					operacaoDecodificarMudancaDeEstado,
					"identificador de lote afirmado excede o comprimento maximo")
			}
			return mudanca, nil
		})
}
