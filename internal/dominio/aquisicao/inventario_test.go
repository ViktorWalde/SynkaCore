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
