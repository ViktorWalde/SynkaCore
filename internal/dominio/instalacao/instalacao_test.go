package instalacao_test

import (
	"testing"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/instalacao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/pontodemedicao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

// A troca de sensor do cenario de teste acontece no meio destas tres datas.
var (
	antesDaTroca  = time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	dataDaTroca   = time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	depoisDaTroca = time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
)

func dispositivo(t *testing.T, nome string) identidadededispositivo.IDDoDispositivo {
	t.Helper()
	id, err := identidadededispositivo.AnalisarIDDoDispositivo(nome)
	if err != nil {
		t.Fatalf("dispositivo de teste invalido: %v", err)
	}
	return id
}

func ponto(t *testing.T, nome string) pontodemedicao.IDDoPontoDeMedicao {
	t.Helper()
	id, err := pontodemedicao.AnalisarIDDoPontoDeMedicao(nome)
	if err != nil {
		t.Fatalf("ponto de teste invalido: %v", err)
	}
	return id
}

func canal(t *testing.T, nomeDoDispositivo string, indice uint32) instalacao.ChaveDeCanal {
	t.Helper()
	return instalacao.ChaveDeCanal{
		Dispositivo: dispositivo(t, nomeDoDispositivo),
		Endereco:    aquisicao.EnderecoDeCanal{IndiceDoCanal: indice},
	}
}

func grandeza(t *testing.T, nome string) instalacao.Grandeza {
	t.Helper()
	resolvida, err := instalacao.AnalisarGrandeza(nome)
	if err != nil {
		t.Fatalf("grandeza de teste invalida: %v", err)
	}
	return resolvida
}

func faixa(valor float64) *float64 { return &valor }

func montar(t *testing.T, parametros instalacao.ParametrosDeInstalacao) *instalacao.Instalacao {
	t.Helper()
	configurada, err := instalacao.NovaInstalacao(parametros)
	if err != nil {
		t.Fatalf("instalacao de teste deveria ser valida: %v", err)
	}
	return configurada
}

// instalacaoDeTeste monta uma configuracao valida de duas grandezas, sem vigencia.
func instalacaoDeTeste(t *testing.T) *instalacao.Instalacao {
	t.Helper()
	return montar(t, instalacao.ParametrosDeInstalacao{
		ID: "planta-piloto",
		Mapeamentos: map[instalacao.ChaveDeCanal][]instalacao.PontoConfigurado{
			canal(t, "camara-01", 0): {{
				Ponto:       ponto(t, "curtimento.camara-01.temperatura"),
				Grandeza:    grandeza(t, "temperatura"),
				Unidade:     "Cel",
				FaixaMinima: faixa(-20),
				FaixaMaxima: faixa(200),
			}},
			canal(t, "camara-01", 1): {{
				Ponto:    ponto(t, "curtimento.camara-01.pressao"),
				Grandeza: grandeza(t, "pressao"),
				Unidade:  "kPa",
			}},
		},
		Motivos:          map[uint32]string{10: "Falta de materia-prima", 40: "Falha eletrica"},
		VersaoDosMotivos: 1,
	})
}

// ---------------------------------------------------------------- grandezas

// TestGrandezasVemDoContratoPorReflexao verifica que nao ha segunda lista.
//
// O mapa de grandezas e construido lendo o descritor do protobuf. Acrescentar uma
// grandeza ao .proto a torna utilizavel na configuracao automaticamente — sem que
// ninguem precise lembrar de atualizar um mapa escrito a mao, que e exatamente o
// esquecimento que produz "o gateway nao reconhece a grandeza que o contrato
// declara".
func TestGrandezasVemDoContratoPorReflexao(t *testing.T) {
	if aceitas := instalacao.GrandezasAceitas(); len(aceitas) < 20 {
		t.Errorf("grandezas aceitas = %d, esperado ao menos 20", len(aceitas))
	}

	// Uma amostra de cada familia declarada no contrato.
	for _, nome := range []string{
		"temperatura", "pressao",
		"ph", "brix", "umidade_de_grao",
		"rotacao", "torque",
		"tensao", "energia_eletrica",
	} {
		if _, err := instalacao.AnalisarGrandeza(nome); err != nil {
			t.Errorf("grandeza %q deveria ser aceita: %v", nome, err)
		}
	}

	if nome := instalacao.NomeDaGrandeza(grandeza(t, "umidade_de_grao")); nome != "umidade_de_grao" {
		t.Errorf("ida e volta = %q, esperado umidade_de_grao", nome)
	}
}

// TestUmidadeDeGraoNaoEUmidadeDoAr protege uma confusao especifica e cara.
//
// Sao grandezas DIFERENTES: uma e conteudo de agua do material, a outra e umidade
// relativa do ambiente. Confundi-las inutiliza qualquer analise de secagem, e o
// nome parecido convida ao erro.
func TestUmidadeDeGraoNaoEUmidadeDoAr(t *testing.T) {
	if grandeza(t, "umidade_de_grao") == grandeza(t, "umidade_do_ar") {
		t.Error("umidade de grao e umidade do ar colapsaram na mesma grandeza")
	}
}

// TestGrandezaDesconhecidaListaAsAceitas verifica a ergonomia do erro.
//
// Quem le esta mensagem e um tecnico comissionando um painel. "Grandeza
// desconhecida" sem a lista o obriga a ir procurar o contrato, que ele nao tem.
func TestGrandezaDesconhecidaListaAsAceitas(t *testing.T) {
	_, err := instalacao.AnalisarGrandeza("temperatura_do_mancal")
	if err == nil {
		t.Fatal("grandeza inexistente deveria ser recusada")
	}
	if !falha.TemCategoria(err, falha.CategoriaEntradaInvalida) {
		t.Errorf("categoria = %v", falha.CategoriaDe(err))
	}
	if len(err.Error()) < 100 {
		t.Errorf("a mensagem deveria listar as aceitas: %q", err.Error())
	}
}

// ---------------------------------------------------------------- resolucao

func TestResolverDevolveOPontoConfigurado(t *testing.T) {
	configurada := instalacaoDeTeste(t)

	configurado, existe := configurada.Resolver(canal(t, "camara-01", 0), depoisDaTroca)
	if !existe {
		t.Fatal("o canal 0 deveria estar configurado")
	}
	if configurado.Ponto.String() != "curtimento.camara-01.temperatura" {
		t.Errorf("ponto = %q", configurado.Ponto)
	}
	if configurado.Unidade != "Cel" {
		t.Errorf("unidade = %q", configurado.Unidade)
	}
}

// TestCanalNaoConfiguradoNaoEFalha documenta a escolha que evita perder dado
// durante o comissionamento.
func TestCanalNaoConfiguradoNaoEFalha(t *testing.T) {
	configurada := instalacaoDeTeste(t)

	if _, existe := configurada.Resolver(canal(t, "camara-01", 99), depoisDaTroca); existe {
		t.Error("canal inexistente nao deveria resolver")
	}
	if _, existe := configurada.Resolver(canal(t, "outro-dispositivo", 0), depoisDaTroca); existe {
		t.Error("canal de outro dispositivo nao deveria resolver")
	}
}

func TestForaDeFaixaMarcaSemRecusar(t *testing.T) {
	configurada := instalacaoDeTeste(t)
	comFaixa, _ := configurada.Resolver(canal(t, "camara-01", 0), depoisDaTroca)
	semFaixa, _ := configurada.Resolver(canal(t, "camara-01", 1), depoisDaTroca)

	if comFaixa.ForaDeFaixa(65.4) {
		t.Error("valor dentro da faixa foi marcado")
	}
	if !comFaixa.ForaDeFaixa(500) || !comFaixa.ForaDeFaixa(-100) {
		t.Error("valor fora da faixa nao foi marcado")
	}

	// Faixa NAO declarada nunca produz anomalia: ausencia de configuracao nao e o
	// mesmo que violacao dela.
	if semFaixa.ForaDeFaixa(999999) {
		t.Error("ponto sem faixa declarada nao deveria produzir anomalia")
	}
}

// ---------------------------------------------------------------- vigencia

// trocaDeSensor monta o cenario central da vigencia: o ponto era alimentado pelo
// canal 0 da camara-01 e passou a ser alimentado pelo canal 0 da camara-02.
func trocaDeSensor(t *testing.T) *instalacao.Instalacao {
	t.Helper()

	daTemperatura := ponto(t, "curtimento.camara-01.temperatura")
	return montar(t, instalacao.ParametrosDeInstalacao{
		ID: "planta-piloto",
		Mapeamentos: map[instalacao.ChaveDeCanal][]instalacao.PontoConfigurado{
			canal(t, "camara-01", 0): {{
				Ponto:      daTemperatura,
				Grandeza:   grandeza(t, "temperatura"),
				Unidade:    "Cel",
				VigenteAte: dataDaTroca,
			}},
			canal(t, "camara-02", 0): {{
				Ponto:     daTemperatura,
				Grandeza:  grandeza(t, "temperatura"),
				Unidade:   "Cel",
				VigenteDe: dataDaTroca,
			}},
		},
	})
}

// TestTrocaDeSensorNaoRompeASerieHistorica e a razao de a vigencia existir.
//
// A serie pertence ao PONTO DE MEDICAO, nao a peca de hardware. Uma leitura
// gravada antes da troca precisa continuar sendo interpretada com a configuracao
// que valia naquele momento — senao reprocessar o diario amanha atribuiria o dado
// de ontem ao lugar errado, e uma serie que muda de significado ao se editar um
// arquivo nao e uma serie confiavel.
func TestTrocaDeSensorNaoRompeASerieHistorica(t *testing.T) {
	configurada := trocaDeSensor(t)

	antigo, existiaAntes := configurada.Resolver(canal(t, "camara-01", 0), antesDaTroca)
	if !existiaAntes {
		t.Fatal("o sensor antigo deveria resolver para o instante anterior a troca")
	}
	if antigo.Ponto.String() != "curtimento.camara-01.temperatura" {
		t.Errorf("ponto antes da troca = %q", antigo.Ponto)
	}

	// Depois da troca, o canal antigo nao alimenta mais nada.
	if _, aindaVale := configurada.Resolver(canal(t, "camara-01", 0), depoisDaTroca); aindaVale {
		t.Error("o sensor antigo continuou resolvendo depois de a vigencia terminar")
	}

	// E o novo nao valia antes de ser instalado.
	if _, valiaAntes := configurada.Resolver(canal(t, "camara-02", 0), antesDaTroca); valiaAntes {
		t.Error("o sensor novo resolveu para um instante anterior a sua instalacao")
	}

	novo, valeDepois := configurada.Resolver(canal(t, "camara-02", 0), depoisDaTroca)
	if !valeDepois {
		t.Fatal("o sensor novo deveria resolver depois da troca")
	}
	if novo.Ponto.String() != "curtimento.camara-01.temperatura" {
		t.Errorf("ponto depois da troca = %q; a serie deveria continuar a mesma", novo.Ponto)
	}
}

// TestVigenciaEIntervaloFechadoAberto congela a semantica da fronteira.
//
// [VigenteDe, VigenteAte): o instante exato da troca ja pertence ao NOVO sensor.
// Sem essa definicao, uma leitura no milissegundo da troca resolveria para os dois
// ou para nenhum, dependendo da ordem em que o codigo olhasse.
func TestVigenciaEIntervaloFechadoAberto(t *testing.T) {
	configurada := trocaDeSensor(t)

	if _, valeParaOAntigo := configurada.Resolver(canal(t, "camara-01", 0), dataDaTroca); valeParaOAntigo {
		t.Error("no instante exato da troca, o sensor ANTIGO nao deveria mais valer")
	}
	if _, valeParaONovo := configurada.Resolver(canal(t, "camara-02", 0), dataDaTroca); !valeParaONovo {
		t.Error("no instante exato da troca, o sensor NOVO ja deveria valer")
	}
}

// TestVinculosDoPontoRespondemQuemAlimentouQuando fecha o objetivo da vigencia.
//
// Esta e a pergunta que a serie sozinha nao responde: "qual dispositivo alimentava
// este ponto em tal instante?" — respondivel anos depois, sem depender de historico
// de git nem da memoria de quem trocou o sensor.
func TestVinculosDoPontoRespondemQuemAlimentouQuando(t *testing.T) {
	configurada := trocaDeSensor(t)
	daTemperatura := ponto(t, "curtimento.camara-01.temperatura")

	vinculos := configurada.VinculosDoPonto(daTemperatura)
	if len(vinculos) != 2 {
		t.Fatalf("vinculos = %d, esperado 2 (antes e depois da troca)", len(vinculos))
	}

	if vinculos[0].IDDoDispositivo().String() != "camara-01" {
		t.Errorf("primeiro vinculo = %q, esperado camara-01", vinculos[0].IDDoDispositivo())
	}
	if vinculos[0].Aberto() {
		t.Error("o vinculo antigo deveria estar encerrado")
	}
	if !vinculos[0].CobreInstante(antesDaTroca) {
		t.Error("o vinculo antigo deveria cobrir o instante anterior a troca")
	}

	if vinculos[1].IDDoDispositivo().String() != "camara-02" {
		t.Errorf("segundo vinculo = %q, esperado camara-02", vinculos[1].IDDoDispositivo())
	}
	if !vinculos[1].Aberto() {
		t.Error("o vinculo novo deveria estar aberto")
	}
	if !vinculos[1].CobreInstante(depoisDaTroca) {
		t.Error("o vinculo novo deveria cobrir o instante posterior a troca")
	}
}

// TestDoisCanaisNoMesmoPontoAoMesmoTempoSaoRecusados protege contra ambiguidade.
//
// Duas leituras indo para a mesma serie produziriam uma oscilacao que nao existe no
// equipamento. Um ponto PODE ter duas fontes ao longo do tempo — isso e a troca de
// sensor — mas nunca simultaneamente.
func TestDoisCanaisNoMesmoPontoAoMesmoTempoSaoRecusados(t *testing.T) {
	daTemperatura := ponto(t, "curtimento.camara-01.temperatura")
	mesmoPonto := instalacao.PontoConfigurado{
		Ponto: daTemperatura, Grandeza: grandeza(t, "temperatura"), Unidade: "Cel",
	}

	_, err := instalacao.NovaInstalacao(instalacao.ParametrosDeInstalacao{
		ID: "planta-piloto",
		Mapeamentos: map[instalacao.ChaveDeCanal][]instalacao.PontoConfigurado{
			canal(t, "camara-01", 0): {mesmoPonto},
			canal(t, "camara-01", 5): {mesmoPonto},
		},
	})
	if err == nil {
		t.Fatal("dois canais alimentando o mesmo ponto ao mesmo tempo deveriam ser recusados")
	}
	if !falha.TemCategoria(err, falha.CategoriaEntradaInvalida) {
		t.Errorf("categoria = %v", falha.CategoriaDe(err))
	}
}

// TestVigenciasSobrepostasNoMesmoCanalSaoRecusadas cobre o outro lado da ambiguidade.
//
// Duas configuracoes valendo ao mesmo tempo para o mesmo canal fariam a
// interpretacao do dado depender de qual delas o codigo encontrasse primeiro.
func TestVigenciasSobrepostasNoMesmoCanalSaoRecusadas(t *testing.T) {
	temperatura := grandeza(t, "temperatura")

	_, err := instalacao.NovaInstalacao(instalacao.ParametrosDeInstalacao{
		ID: "planta-piloto",
		Mapeamentos: map[instalacao.ChaveDeCanal][]instalacao.PontoConfigurado{
			canal(t, "camara-01", 0): {
				{
					Ponto:    ponto(t, "curtimento.camara-01.temperatura"),
					Grandeza: temperatura, Unidade: "Cel",
					VigenteAte: depoisDaTroca,
				},
				{
					Ponto:    ponto(t, "curtimento.camara-01.temperatura-nova"),
					Grandeza: temperatura, Unidade: "Cel",
					VigenteDe: dataDaTroca, // comeca ANTES de a anterior terminar
				},
			},
		},
	})
	if err == nil {
		t.Fatal("vigencias sobrepostas no mesmo canal deveriam ser recusadas")
	}
}

// ---------------------------------------------------------------- validacao

func TestConfiguracaoIncompletaERecusadaNaPartida(t *testing.T) {
	temperatura := grandeza(t, "temperatura")
	valido := instalacao.PontoConfigurado{
		Ponto: ponto(t, "curtimento.camara-01.temperatura"), Grandeza: temperatura, Unidade: "Cel",
	}
	umCanal := canal(t, "camara-01", 0)

	casos := map[string]instalacao.ParametrosDeInstalacao{
		"sem identificador de instalacao": {
			Mapeamentos: map[instalacao.ChaveDeCanal][]instalacao.PontoConfigurado{umCanal: {valido}},
		},
		"sem nenhum ponto": {
			ID:          "planta-piloto",
			Mapeamentos: map[instalacao.ChaveDeCanal][]instalacao.PontoConfigurado{},
		},
		"canal sem mapeamento nenhum": {
			ID:          "planta-piloto",
			Mapeamentos: map[instalacao.ChaveDeCanal][]instalacao.PontoConfigurado{umCanal: {}},
		},
		"ponto sem grandeza": {
			ID: "planta-piloto",
			Mapeamentos: map[instalacao.ChaveDeCanal][]instalacao.PontoConfigurado{
				umCanal: {{Ponto: ponto(t, "x.y"), Unidade: "Cel"}},
			},
		},
		"ponto sem unidade": {
			ID: "planta-piloto",
			Mapeamentos: map[instalacao.ChaveDeCanal][]instalacao.PontoConfigurado{
				umCanal: {{Ponto: ponto(t, "x.y"), Grandeza: temperatura}},
			},
		},
		"faixa invertida": {
			ID: "planta-piloto",
			Mapeamentos: map[instalacao.ChaveDeCanal][]instalacao.PontoConfigurado{
				umCanal: {{
					Ponto: ponto(t, "x.y"), Grandeza: temperatura, Unidade: "Cel",
					FaixaMinima: faixa(200), FaixaMaxima: faixa(-20),
				}},
			},
		},
		"vigencia invertida": {
			ID: "planta-piloto",
			Mapeamentos: map[instalacao.ChaveDeCanal][]instalacao.PontoConfigurado{
				umCanal: {{
					Ponto: ponto(t, "x.y"), Grandeza: temperatura, Unidade: "Cel",
					VigenteDe: depoisDaTroca, VigenteAte: antesDaTroca,
				}},
			},
		},
		"codigo de motivo zero, que e reservado": {
			ID:          "planta-piloto",
			Mapeamentos: map[instalacao.ChaveDeCanal][]instalacao.PontoConfigurado{umCanal: {valido}},
			Motivos:     map[uint32]string{0: "nao classificada"},
		},
	}

	for nome, parametros := range casos {
		t.Run(nome, func(t *testing.T) {
			if _, err := instalacao.NovaInstalacao(parametros); err == nil {
				t.Fatal("configuracao invalida deveria ser recusada na partida")
			}
		})
	}
}

// TestMotivoNaoClassificadoEReconhecido protege a distincao entre "nao sei" e
// "dado faltando".
func TestMotivoNaoClassificadoEReconhecido(t *testing.T) {
	configurada := instalacaoDeTeste(t)

	rotulo, reconhecido := configurada.RotuloDoMotivo(0)
	if !reconhecido {
		t.Error("o codigo zero e um valor previsto do vocabulario, nao uma violacao dele")
	}
	if rotulo != "" {
		t.Errorf("rotulo do codigo zero = %q, esperado vazio", rotulo)
	}

	if rotulo, reconhecido := configurada.RotuloDoMotivo(40); !reconhecido || rotulo != "Falha eletrica" {
		t.Errorf("codigo 40 = %q (reconhecido=%v)", rotulo, reconhecido)
	}
	if _, reconhecido := configurada.RotuloDoMotivo(999); reconhecido {
		t.Error("codigo fora do catalogo nao deveria ser reconhecido")
	}
}
