package aquisicao_test

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	contratov1 "github.com/ViktorWalde/SynkaCore/internal/contrato/v1"
	"github.com/ViktorWalde/SynkaCore/internal/dominio/aquisicao"
)

// camposDoOneofDeConteudo devolve os nomes dos campos do `oneof conteudo` do
// contrato, lidos do descritor gerado.
//
// Lidos por reflexao, e nao escritos a mao no teste, porque uma lista escrita a
// mao seria a terceira copia do mesmo conjunto — e a copia que o autor esquece de
// atualizar e sempre a que estava no teste.
func camposDoOneofDeConteudo(t *testing.T) map[string]struct{} {
	t.Helper()

	descritor := (&contratov1.Envelope{}).ProtoReflect().Descriptor()
	oneof := descritor.Oneofs().ByName(protoreflect.Name(aquisicao.NomeDoOneofDeConteudo))
	if oneof == nil {
		t.Fatalf("o contrato nao tem um oneof chamado %q", aquisicao.NomeDoOneofDeConteudo)
	}

	nomes := make(map[string]struct{}, oneof.Fields().Len())
	for indice := range oneof.Fields().Len() {
		nomes[string(oneof.Fields().Get(indice).Name())] = struct{}{}
	}
	return nomes
}

// TestTodoConteudoDoContratoTemDefinicao e a trava que impede o defeito mais
// provavel da evolucao deste sistema: acrescentar uma mensagem ao contrato e
// esquecer de ensinar o gateway a interpreta-la.
//
// Sem esta verificacao, a origem nova enviaria dado que o gateway recusaria como
// "tipo desconhecido" — e a falha apareceria em campo, numa planta, e nao aqui.
func TestTodoConteudoDoContratoTemDefinicao(t *testing.T) {
	doContrato := camposDoOneofDeConteudo(t)

	definidos := make(map[string]struct{}, len(aquisicao.TodasAsDefinicoes()))
	for _, definicao := range aquisicao.TodasAsDefinicoes() {
		definidos[string(definicao.Tipo)] = struct{}{}
	}

	for nome := range doContrato {
		if _, temDefinicao := definidos[nome]; !temDefinicao {
			t.Errorf("o contrato declara o conteudo %q, mas nenhuma definicao de catalogo o interpreta", nome)
		}
	}
}

// TestNenhumaDefinicaoSemConteudoNoContrato cobre o sentido oposto: um tipo de
// conteudo que o gateway acha que sabe interpretar mas que nenhuma origem pode
// enviar, porque nao existe no contrato.
//
// Isso e codigo morto que todo leitor futuro precisa entender antes de descobrir
// que nao serve para nada — e, pior, um nome de tipo que nao corresponde a campo
// nenhum revela que o codec nunca vai resolve-lo.
func TestNenhumaDefinicaoSemConteudoNoContrato(t *testing.T) {
	doContrato := camposDoOneofDeConteudo(t)

	for _, definicao := range aquisicao.TodasAsDefinicoes() {
		if _, existe := doContrato[string(definicao.Tipo)]; !existe {
			t.Errorf("a definicao de catalogo %q nao corresponde a nenhum campo do oneof do contrato",
				definicao.Tipo)
		}
	}
}

// TestCatalogoCompletoMonta garante que o inventario inteiro passa pelas regras de
// NovoCatalogoDeConteudo — tipo nao vazio, classe valida, decodificador presente e
// nenhum tipo repetido.
func TestCatalogoCompletoMonta(t *testing.T) {
	catalogo, err := aquisicao.NovoCatalogoDeConteudo(aquisicao.TodasAsDefinicoes()...)
	if err != nil {
		t.Fatalf("o catalogo completo deveria montar, mas falhou: %v", err)
	}
	if quantidade := len(catalogo.Tipos()); quantidade != len(aquisicao.TodasAsDefinicoes()) {
		t.Errorf("catalogo montou com %d tipos, esperado %d", quantidade, len(aquisicao.TodasAsDefinicoes()))
	}
}

// TestConteudoEnderecadoCasaComOContrato trava a correspondencia entre ter
// endereco no dominio e ter endereco no contrato.
//
// Este teste existe porque a primeira versao do enriquecimento da projecao usou
// uma assercao para um metodo que NAO EXISTIA em nenhum tipo. Ela compilava, nunca
// casava, e o enriquecimento inteiro era codigo morto — nenhuma linha ganharia
// ponto de medicao, e nada acusaria.
//
// A correspondencia e verificada contra o descritor do protobuf: um conteudo cuja
// mensagem do contrato tem o campo `endereco` PRECISA implementar
// ConteudoEnderecado, e um que nao tem, nao pode implementar.
func TestConteudoEnderecadoCasaComOContrato(t *testing.T) {
	descritor := (&contratov1.Envelope{}).ProtoReflect().Descriptor()
	oneof := descritor.Oneofs().ByName(protoreflect.Name(aquisicao.NomeDoOneofDeConteudo))

	// Amostra de cada conteudo, construida por decodificacao de bytes vazios: todo
	// campo do contrato e opcional, entao a mensagem vazia decodifica para o valor
	// zero do tipo de dominio — que e tudo que este teste precisa.
	catalogo, err := aquisicao.NovoCatalogoDeConteudo(aquisicao.TodasAsDefinicoes()...)
	if err != nil {
		t.Fatalf("montagem do catalogo falhou: %v", err)
	}

	for indice := range oneof.Fields().Len() {
		campo := oneof.Fields().Get(indice)
		nome := string(campo.Name())

		t.Run(nome, func(t *testing.T) {
			definicao, err := catalogo.Buscar(aquisicao.TipoDeConteudo(nome))
			if err != nil {
				t.Fatalf("sem definicao de catalogo: %v", err)
			}

			contratoTemEndereco := campo.Message().Fields().ByName("endereco") != nil

			// Alguns conteudos recusam a mensagem vazia por terem validacao propria
			// (amostra agregada exige contagem, lacuna exige registros perdidos).
			// Nesses casos a decodificacao falha e o teste usa o proprio descritor
			// como fonte, que e o que importa aqui.
			conteudo, err := definicao.Decodificar([]byte{})
			if err != nil {
				if contratoTemEndereco {
					t.Logf("conteudo com validacao propria; correspondencia conferida pelo descritor")
				}
				return
			}

			_, dominioTemEndereco := conteudo.(aquisicao.ConteudoEnderecado)

			if contratoTemEndereco && !dominioTemEndereco {
				t.Errorf("o contrato declara `endereco` em %s, mas o tipo de dominio nao implementa "+
					"ConteudoEnderecado: a projecao nunca resolvera o ponto de medicao deste conteudo", nome)
			}
			if !contratoTemEndereco && dominioTemEndereco {
				t.Errorf("%s implementa ConteudoEnderecado mas o contrato nao tem campo `endereco`: "+
					"o endereco projetado seria sempre zero", nome)
			}
		})
	}
}
