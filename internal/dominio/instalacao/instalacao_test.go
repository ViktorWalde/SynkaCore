package instalacao_test

import (
	"testing"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/instalacao"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/pontodemedicao"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
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

func faixa(valor float64) *float64 { return &valor }

// instalacaoDeTeste monta uma configuracao valida de duas grandezas.
func instalacaoDeTeste(t *testing.T) *instalacao.Instalacao {
	t.Helper()

	temperatura, err := instalacao.AnalisarGrandeza("temperatura")
	if err != nil {
		t.Fatalf("grandeza de teste invalida: %v", err)
	}
	pressao, err := instalacao.AnalisarGrandeza("pressao")
	if err != nil {
		t.Fatalf("grandeza de teste invalida: %v", err)
	}

	configurada, err := instalacao.NovaInstalacao(instalacao.ParametrosDeInstalacao{
		ID: "planta-piloto",
		Pontos: map[instalacao.ChaveDeCanal]instalacao.PontoConfigurado{
			canal(t, "camara-01", 0): {
				Ponto:       ponto(t, "curtimento.camara-01.temperatura"),
				Grandeza:    temperatura,
				Unidade:     "Cel",
				FaixaMinima: faixa(-20),
				FaixaMaxima: faixa(200),
			},
			canal(t, "camara-01", 1): {
				Ponto:    ponto(t, "curtimento.camara-01.pressao"),
				Grandeza: pressao,
				Unidade:  "kPa",
			},
		},
		Motivos:          map[uint32]string{10: "Falta de materia-prima", 40: "Falha eletrica"},
		VersaoDosMotivos: 1,
	})
	if err != nil {
		t.Fatalf("instalacao de teste deveria ser valida: %v", err)
	}
	return configurada
}

// TestGrandezasVemDoContratoPorReflexao verifica que nao ha segunda lista.
//
// O mapa de grandezas e construido lendo o descritor do protobuf. Acrescentar uma
// grandeza ao .proto a torna utilizavel na configuracao automaticamente — sem que
// ninguem precise lembrar de atualizar um mapa escrito a mao, que e exatamente o
// esquecimento que produz "o gateway nao reconhece a grandeza que o contrato
// declara".
func TestGrandezasVemDoContratoPorReflexao(t *testing.T) {
	aceitas := instalacao.GrandezasAceitas()

	if len(aceitas) < 20 {
		t.Errorf("grandezas aceitas = %d, esperado ao menos 20 (o contrato declara mais que isso)",
			len(aceitas))
	}

	// Uma amostra de cada familia declarada no contrato.
	for _, nome := range []string{
		"temperatura", "pressao", // processo
		"ph", "brix", "umidade_de_grao", // agroindustria
		"rotacao", "torque", // mecanicas
		"tensao", "energia_eletrica", // eletricas
	} {
		if _, err := instalacao.AnalisarGrandeza(nome); err != nil {
			t.Errorf("grandeza %q deveria ser aceita: %v", nome, err)
		}
	}

	// E a ida e volta preserva o nome.
	grandeza, err := instalacao.AnalisarGrandeza("umidade_de_grao")
	if err != nil {
		t.Fatalf("analise falhou: %v", err)
	}
	if nome := instalacao.NomeDaGrandeza(grandeza); nome != "umidade_de_grao" {
		t.Errorf("nome = %q, esperado umidade_de_grao", nome)
	}
}

// TestUmidadeDeGraoNaoEUmidadeDoAr protege uma confusao especifica e cara.
//
// Sao grandezas DIFERENTES: uma e conteudo de agua do material, a outra e umidade
// relativa do ambiente. Confundi-las inutiliza qualquer analise de secagem, e o
// nome parecido convida ao erro.
func TestUmidadeDeGraoNaoEUmidadeDoAr(t *testing.T) {
	doGrao, err := instalacao.AnalisarGrandeza("umidade_de_grao")
	if err != nil {
		t.Fatalf("analise falhou: %v", err)
	}
	doAr, err := instalacao.AnalisarGrandeza("umidade_do_ar")
	if err != nil {
		t.Fatalf("analise falhou: %v", err)
	}
	if doGrao == doAr {
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
		t.Errorf("a mensagem deveria listar as grandezas aceitas, mas tem %d caracteres: %q",
			len(err.Error()), err.Error())
	}
}

func TestResolverDevolveOPontoConfigurado(t *testing.T) {
	configurada := instalacaoDeTeste(t)

	configurado, existe := configurada.Resolver(canal(t, "camara-01", 0))
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
//
// Uma origem enviando canal que ninguem mapeou ainda e o caso NORMAL enquanto a
// instalacao esta sendo montada. Tratar isso como erro faria o gateway recusar
// dado exatamente na fase em que ele mais e produzido.
func TestCanalNaoConfiguradoNaoEFalha(t *testing.T) {
	configurada := instalacaoDeTeste(t)

	if _, existe := configurada.Resolver(canal(t, "camara-01", 99)); existe {
		t.Error("canal inexistente nao deveria resolver")
	}
	if _, existe := configurada.Resolver(canal(t, "outro-dispositivo", 0)); existe {
		t.Error("canal de outro dispositivo nao deveria resolver")
	}
}

// TestForaDeFaixaMarcaSemRecusar cobre a politica de faixa plausivel.
func TestForaDeFaixaMarcaSemRecusar(t *testing.T) {
	configurada := instalacaoDeTeste(t)
	comFaixa, _ := configurada.Resolver(canal(t, "camara-01", 0))
	semFaixa, _ := configurada.Resolver(canal(t, "camara-01", 1))

	if comFaixa.ForaDeFaixa(65.4) {
		t.Error("valor dentro da faixa foi marcado")
	}
	if !comFaixa.ForaDeFaixa(500) {
		t.Error("valor acima do maximo nao foi marcado")
	}
	if !comFaixa.ForaDeFaixa(-100) {
		t.Error("valor abaixo do minimo nao foi marcado")
	}

	// Faixa NAO declarada nunca produz anomalia: ausencia de configuracao nao e o
	// mesmo que violacao dela, e tratar as duas igual encheria de alarme falso toda
	// instalacao ainda incompleta.
	if semFaixa.ForaDeFaixa(999999) {
		t.Error("ponto sem faixa declarada nao deveria produzir anomalia")
	}
}

// TestDoisCanaisNoMesmoPontoSaoRecusados protege contra ambiguidade silenciosa.
//
// Duas leituras indo para a mesma serie produziriam uma oscilacao que nao existe
// no equipamento — e o grafico pareceria mostrar um processo instavel.
func TestDoisCanaisNoMesmoPontoSaoRecusados(t *testing.T) {
	temperatura, _ := instalacao.AnalisarGrandeza("temperatura")

	_, err := instalacao.NovaInstalacao(instalacao.ParametrosDeInstalacao{
		ID: "planta-piloto",
		Pontos: map[instalacao.ChaveDeCanal]instalacao.PontoConfigurado{
			canal(t, "camara-01", 0): {
				Ponto: ponto(t, "curtimento.camara-01.temperatura"), Grandeza: temperatura, Unidade: "Cel",
			},
			canal(t, "camara-01", 5): {
				Ponto: ponto(t, "curtimento.camara-01.temperatura"), Grandeza: temperatura, Unidade: "Cel",
			},
		},
	})
	if err == nil {
		t.Fatal("dois canais alimentando o mesmo ponto deveriam ser recusados")
	}
	if !falha.TemCategoria(err, falha.CategoriaEntradaInvalida) {
		t.Errorf("categoria = %v", falha.CategoriaDe(err))
	}
}

func TestConfiguracaoIncompletaERecusadaNaPartida(t *testing.T) {
	temperatura, _ := instalacao.AnalisarGrandeza("temperatura")
	valido := instalacao.PontoConfigurado{
		Ponto: ponto(t, "curtimento.camara-01.temperatura"), Grandeza: temperatura, Unidade: "Cel",
	}

	casos := map[string]instalacao.ParametrosDeInstalacao{
		"sem identificador de instalacao": {
			Pontos: map[instalacao.ChaveDeCanal]instalacao.PontoConfigurado{canal(t, "camara-01", 0): valido},
		},
		"sem nenhum ponto": {
			ID:     "planta-piloto",
			Pontos: map[instalacao.ChaveDeCanal]instalacao.PontoConfigurado{},
		},
		"ponto sem grandeza": {
			ID: "planta-piloto",
			Pontos: map[instalacao.ChaveDeCanal]instalacao.PontoConfigurado{
				canal(t, "camara-01", 0): {Ponto: ponto(t, "x.y"), Unidade: "Cel"},
			},
		},
		"ponto sem unidade": {
			ID: "planta-piloto",
			Pontos: map[instalacao.ChaveDeCanal]instalacao.PontoConfigurado{
				canal(t, "camara-01", 0): {Ponto: ponto(t, "x.y"), Grandeza: temperatura},
			},
		},
		"faixa invertida": {
			ID: "planta-piloto",
			Pontos: map[instalacao.ChaveDeCanal]instalacao.PontoConfigurado{
				canal(t, "camara-01", 0): {
					Ponto: ponto(t, "x.y"), Grandeza: temperatura, Unidade: "Cel",
					FaixaMinima: faixa(200), FaixaMaxima: faixa(-20),
				},
			},
		},
		"codigo de motivo zero, que e reservado": {
			ID: "planta-piloto",
			Pontos: map[instalacao.ChaveDeCanal]instalacao.PontoConfigurado{
				canal(t, "camara-01", 0): valido,
			},
			Motivos: map[uint32]string{0: "nao classificada"},
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
//
// O codigo zero significa parada AINDA NAO CLASSIFICADA. O operador pode nao ter
// classificado, e forcar uma classificacao errada e pior que admitir a falta.
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
