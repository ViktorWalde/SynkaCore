package projecao

// Teste DENTRO do package, e nao em projecao_test.
//
// O enriquecimento e uma funcao nao exportada, e exercita-la de fora exigiria um
// banco de consulta de verdade — o que transformaria o teste da regra central da
// V2.2 num teste de integracao que so roda onde ha Postgres. A regra e pura: ela
// so consulta a configuracao e o conteudo decodificado.

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/adaptador/saida/projetortimescale"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/instalacao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/pontodemedicao"
)

const dispositivoDeTeste = "camara-01"

var instanteDeReferencia = time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)

func faixa(valor float64) *float64 { return &valor }

func servicoDeTeste(t *testing.T, comConfiguracao bool) *Servico {
	t.Helper()

	servico := &Servico{
		registro: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	if !comConfiguracao {
		return servico
	}

	dispositivo, err := identidadededispositivo.AnalisarIDDoDispositivo(dispositivoDeTeste)
	if err != nil {
		t.Fatalf("dispositivo invalido: %v", err)
	}
	temperatura, err := instalacao.AnalisarGrandeza("temperatura")
	if err != nil {
		t.Fatalf("grandeza invalida: %v", err)
	}
	ponto, err := pontodemedicao.AnalisarIDDoPontoDeMedicao("curtimento.camara-01.temperatura")
	if err != nil {
		t.Fatalf("ponto invalido: %v", err)
	}

	configuracao, err := instalacao.NovaInstalacao(instalacao.ParametrosDeInstalacao{
		ID: "planta-teste",
		Mapeamentos: map[instalacao.ChaveDeCanal][]instalacao.PontoConfigurado{
			{Dispositivo: dispositivo, Endereco: aquisicao.EnderecoDeCanal{IndiceDoCanal: 0}}: {{
				Ponto:       ponto,
				Grandeza:    temperatura,
				Unidade:     "Cel",
				FaixaMinima: faixa(-20),
				FaixaMaxima: faixa(200),
			}},
		},
		Motivos:          map[uint32]string{40: "Falha eletrica"},
		VersaoDosMotivos: 1,
	})
	if err != nil {
		t.Fatalf("configuracao invalida: %v", err)
	}
	return servico.ComInstalacao(configuracao)
}

func enriquecer(t *testing.T, servico *Servico,
	conteudo aquisicao.ConteudoDecodificado) projetortimescale.LinhaProjetada {
	t.Helper()

	// O instante observado importa: a resolucao e por VIGENCIA, e uma linha sem
	// instante cairia no tempo zero, antes de qualquer vigencia declarada.
	linha := projetortimescale.LinhaProjetada{
		IDDoDispositivo:   dispositivoDeTeste,
		InstanteObservado: instanteDeReferencia,
	}
	servico.enriquecer(&linha, dispositivoDeTeste, conteudo)
	return linha
}

// TestCanalConfiguradoGanhaSignificado e o que a V2.2 existe para entregar.
func TestCanalConfiguradoGanhaSignificado(t *testing.T) {
	linha := enriquecer(t, servicoDeTeste(t, true), aquisicao.AmostraEscalar{
		Endereco: aquisicao.EnderecoDeCanal{IndiceDoCanal: 0},
		Valor:    65.4,
	})

	if linha.IDDoPontoDeMedicao == nil || *linha.IDDoPontoDeMedicao != "curtimento.camara-01.temperatura" {
		t.Errorf("ponto de medicao = %v", linha.IDDoPontoDeMedicao)
	}
	if linha.Grandeza == nil || *linha.Grandeza != "temperatura" {
		t.Errorf("grandeza = %v", linha.Grandeza)
	}
	if linha.Unidade == nil || *linha.Unidade != "Cel" {
		t.Errorf("unidade = %v", linha.Unidade)
	}
}

// TestForaDeFaixaDistingueTresEstados protege a diferenca entre nulo e falso.
//
// Nulo e "sem faixa configurada"; falso e "dentro da faixa". Colapsar os dois faria
// uma instalacao ainda nao configurada parecer inteiramente saudavel — que e
// justamente a conclusao errada durante o comissionamento.
func TestForaDeFaixaDistingueTresEstados(t *testing.T) {
	comConfiguracao := servicoDeTeste(t, true)

	dentro := enriquecer(t, comConfiguracao, aquisicao.AmostraEscalar{
		Endereco: aquisicao.EnderecoDeCanal{IndiceDoCanal: 0}, Valor: 65.4})
	if dentro.ForaDeFaixa == nil || *dentro.ForaDeFaixa {
		t.Errorf("valor dentro da faixa: fora_de_faixa = %v, esperado falso", dentro.ForaDeFaixa)
	}

	fora := enriquecer(t, comConfiguracao, aquisicao.AmostraEscalar{
		Endereco: aquisicao.EnderecoDeCanal{IndiceDoCanal: 0}, Valor: 900})
	if fora.ForaDeFaixa == nil || !*fora.ForaDeFaixa {
		t.Errorf("valor fora da faixa: fora_de_faixa = %v, esperado verdadeiro", fora.ForaDeFaixa)
	}

	semConfiguracao := enriquecer(t, servicoDeTeste(t, false), aquisicao.AmostraEscalar{
		Endereco: aquisicao.EnderecoDeCanal{IndiceDoCanal: 0}, Valor: 900})
	if semConfiguracao.ForaDeFaixa != nil {
		t.Errorf("sem configuracao: fora_de_faixa = %v, esperado nulo", *semConfiguracao.ForaDeFaixa)
	}
}

// TestCanalNaoConfiguradoEGravadoSemSignificado documenta a escolha que evita
// perder dado durante o comissionamento.
func TestCanalNaoConfiguradoEGravadoSemSignificado(t *testing.T) {
	linha := enriquecer(t, servicoDeTeste(t, true), aquisicao.AmostraEscalar{
		Endereco: aquisicao.EnderecoDeCanal{IndiceDoCanal: 99},
		Valor:    65.4,
	})

	if linha.IDDoPontoDeMedicao != nil {
		t.Errorf("canal nao configurado resolveu para %q", *linha.IDDoPontoDeMedicao)
	}
	// E a linha continua sendo projetada: o dado nao se perde por falta de configuracao.
	if linha.IDDoDispositivo != dispositivoDeTeste {
		t.Error("a linha deveria ser projetada mesmo sem configuracao do canal")
	}
}

// TestConteudoSemEnderecoNaoTentaResolver cobre saude, lacuna e descritor.
//
// Os tres descrevem o DISPOSITIVO, nao um canal. Nao ter ponto de medicao e a
// afirmacao correta sobre eles, e nao uma lacuna de configuracao.
func TestConteudoSemEnderecoNaoTentaResolver(t *testing.T) {
	conteudos := map[string]aquisicao.ConteudoDecodificado{
		"saude da origem":  aquisicao.SaudeDaOrigem{},
		"lacuna de buffer": aquisicao.LacunaDeBuffer{RegistrosPerdidos: 3},
		"descritor":        aquisicao.DescritorDaOrigem{VersaoDoFirmware: "1.0"},
	}

	for nome, conteudo := range conteudos {
		t.Run(nome, func(t *testing.T) {
			if linha := enriquecer(t, servicoDeTeste(t, true), conteudo); linha.IDDoPontoDeMedicao != nil {
				t.Errorf("conteudo sem endereco resolveu para %q", *linha.IDDoPontoDeMedicao)
			}
		})
	}
}

// TestMotivoForaDoCatalogoPreservaOEvento e a suavizacao deliberada da regra
// original, e o teste existe para que ela nao seja "corrigida" por engano.
//
// A arquitetura de origem mandava recusar codigo fora do catalogo. Parada de
// maquina e evento discreto — a classe que NAO tolera perda. Rejeitar o evento
// inteiro por causa de um rotulo desconhecido perderia o FATO para preservar a
// consistencia do vocabulario, e o fato e o que o relatorio conta.
func TestMotivoForaDoCatalogoPreservaOEvento(t *testing.T) {
	linha := enriquecer(t, servicoDeTeste(t, true), aquisicao.MudancaDeEstadoDeMaquina{
		Endereco:       aquisicao.EnderecoDeCanal{IndiceDoCanal: 0},
		Estado:         aquisicao.EstadoQuebra,
		CodigoDoMotivo: 999,
	})

	// O evento foi enriquecido normalmente; apenas o rotulo ficou por resolver.
	if linha.IDDoPontoDeMedicao == nil {
		t.Error("o evento perdeu o ponto de medicao por causa de um motivo desconhecido")
	}
	if linha.RotuloDoMotivo != nil {
		t.Errorf("rotulo = %q, esperado nulo para codigo fora do catalogo", *linha.RotuloDoMotivo)
	}
}

func TestMotivoConhecidoEResolvido(t *testing.T) {
	linha := enriquecer(t, servicoDeTeste(t, true), aquisicao.MudancaDeEstadoDeMaquina{
		Endereco:       aquisicao.EnderecoDeCanal{IndiceDoCanal: 0},
		Estado:         aquisicao.EstadoQuebra,
		CodigoDoMotivo: 40,
	})

	if linha.RotuloDoMotivo == nil || *linha.RotuloDoMotivo != "Falha eletrica" {
		t.Errorf("rotulo = %v, esperado \"Falha eletrica\"", linha.RotuloDoMotivo)
	}
}

// TestParadaNaoClassificadaNaoEAnomalia separa "nao sei" de "codigo invalido".
//
// O codigo zero e um valor PREVISTO do vocabulario: o operador pode nao ter
// classificado, e forcar uma classificacao errada e pior que admitir a falta.
func TestParadaNaoClassificadaNaoEAnomalia(t *testing.T) {
	linha := enriquecer(t, servicoDeTeste(t, true), aquisicao.MudancaDeEstadoDeMaquina{
		Endereco:       aquisicao.EnderecoDeCanal{IndiceDoCanal: 0},
		Estado:         aquisicao.EstadoParada,
		CodigoDoMotivo: 0,
	})

	if linha.RotuloDoMotivo != nil {
		t.Errorf("rotulo = %q, esperado nulo para parada nao classificada", *linha.RotuloDoMotivo)
	}
	if linha.IDDoPontoDeMedicao == nil {
		t.Error("o evento deveria ter sido enriquecido normalmente")
	}
}

// TestSemConfiguracaoNadaEEnriquecido cobre a operacao antes do comissionamento.
func TestSemConfiguracaoNadaEEnriquecido(t *testing.T) {
	linha := enriquecer(t, servicoDeTeste(t, false), aquisicao.AmostraEscalar{
		Endereco: aquisicao.EnderecoDeCanal{IndiceDoCanal: 0}, Valor: 65.4})

	if linha.IDDoPontoDeMedicao != nil || linha.Grandeza != nil || linha.Unidade != nil {
		t.Error("sem configuracao, nenhum campo derivado deveria ser preenchido")
	}
}
