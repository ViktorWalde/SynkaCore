package credencial_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/credencial"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

// certificadoCom monta um certificado de cliente com o nome comum indicado.
func certificadoCom(t *testing.T, nomeComum string) *tls.ConnectionState {
	t.Helper()

	chave, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("geracao da chave falhou: %v", err)
	}

	modelo := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: nomeComum},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	bruto, err := x509.CreateCertificate(rand.Reader, &modelo, &modelo, &chave.PublicKey, chave)
	if err != nil {
		t.Fatalf("emissao falhou: %v", err)
	}
	certificado, err := x509.ParseCertificate(bruto)
	if err != nil {
		t.Fatalf("leitura falhou: %v", err)
	}

	return &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificado}}
}

func dispositivo(t *testing.T, nome string) identidadededispositivo.IDDoDispositivo {
	t.Helper()
	id, err := identidadededispositivo.AnalisarIDDoDispositivo(nome)
	if err != nil {
		t.Fatalf("dispositivo de teste invalido: %v", err)
	}
	return id
}

// TestIdentidadeVemDoNomeComumDoCertificado cobre o caminho normal.
func TestIdentidadeVemDoNomeComumDoCertificado(t *testing.T) {
	autenticada, err := credencial.IdentidadeAutenticada(certificadoCom(t, "esp32-sala-01"))
	if err != nil {
		t.Fatalf("certificado valido deveria autenticar: %v", err)
	}
	if autenticada.String() != "esp32-sala-01" {
		t.Errorf("identidade = %q", autenticada)
	}
}

// TestSePassarPorOutroDispositivoERecusado e o teste central da V2.1.
//
// O cenario: um dispositivo com credencial LEGITIMA escreve na remessa o
// identificador do vizinho. O TLS aceita a conexao — o certificado e valido e foi
// assinado pela CA da instalacao.
//
// Sem esta conferencia, o dado seria gravado sob a identidade errada. E o pior nao e
// a gravacao: e que o resultado e PLAUSIVEL. Nao ha nada no registro que denuncie a
// troca depois, e nenhuma auditoria posterior a recupera.
func TestSePassarPorOutroDispositivoERecusado(t *testing.T) {
	autenticada := dispositivo(t, "impostor-01")

	err := credencial.ConferirIdentidade(autenticada, "camara-de-vacuo-01")
	if err == nil {
		t.Fatal("remessa reivindicando outro dispositivo deveria ser recusada")
	}

	// PermissaoNegada, e nao NaoAutenticado, e a distincao decide o comportamento da
	// origem: a credencial e valida, o portador simplesmente nao pode falar por
	// aquele identificador. NaoAutenticado a faria tentar renovar credencial, que
	// nao resolveria nada.
	if !falha.TemCategoria(err, falha.CategoriaPermissaoNegada) {
		t.Errorf("categoria = %v, esperado CategoriaPermissaoNegada", falha.CategoriaDe(err))
	}

	// A mensagem nomeia AS DUAS identidades. Sem isso, o operador saberia que houve
	// divergencia e nao qual dispositivo esta se passando por qual.
	for _, esperado := range []string{"impostor-01", "camara-de-vacuo-01"} {
		if !strings.Contains(err.Error(), esperado) {
			t.Errorf("a mensagem nao nomeia %q: %v", esperado, err)
		}
	}
}

func TestIdentidadeQueConfereEAceita(t *testing.T) {
	autenticada := dispositivo(t, "camara-de-vacuo-01")

	if err := credencial.ConferirIdentidade(autenticada, "camara-de-vacuo-01"); err != nil {
		t.Errorf("identidade que confere deveria ser aceita: %v", err)
	}
}

// TestRemessaSemIdentificadorERecusada fecha a porta do "nao reivindicar nada".
//
// Sem esta checagem, omitir o identificador escaparia da conferencia — e um atacante
// aprenderia rapido que basta nao afirmar identidade para nao ser conferido.
func TestRemessaSemIdentificadorERecusada(t *testing.T) {
	err := credencial.ConferirIdentidade(dispositivo(t, "camara-de-vacuo-01"), "")
	if err == nil {
		t.Fatal("remessa sem identificador deveria ser recusada")
	}
	if !falha.TemCategoria(err, falha.CategoriaEntradaInvalida) {
		t.Errorf("categoria = %v", falha.CategoriaDe(err))
	}
}

// TestCertificadoInutilizavelComoIdentidadeERecusado cobre os certificados que
// autenticam mas nao identificam.
func TestCertificadoInutilizavelComoIdentidadeERecusado(t *testing.T) {
	casos := map[string]*tls.ConnectionState{
		"sem nome comum":           certificadoCom(t, ""),
		"nome comum com maiuscula": certificadoCom(t, "ESP32-Sala-01"),
		"nome comum com espaco":    certificadoCom(t, "esp32 sala 01"),
	}

	for nome, estado := range casos {
		t.Run(nome, func(t *testing.T) {
			if _, err := credencial.IdentidadeAutenticada(estado); err == nil {
				t.Fatal("certificado que nao identifica deveria ser recusado")
			} else if !falha.TemCategoria(err, falha.CategoriaNaoAutenticado) {
				t.Errorf("categoria = %v, esperado CategoriaNaoAutenticado", falha.CategoriaDe(err))
			}
		})
	}
}

// TestConexaoSemCertificadoECulpaDoGateway verifica a classificacao.
//
// RequireAndVerifyClientCert ja recusa a conexao antes do handler, entao chegar aqui
// sem certificado significa servidor mal configurado — defeito nosso, nao do chamador.
func TestConexaoSemCertificadoECulpaDoGateway(t *testing.T) {
	for nome, estado := range map[string]*tls.ConnectionState{
		"sem estado de TLS":      nil,
		"sem certificado de par": {},
	} {
		t.Run(nome, func(t *testing.T) {
			if _, err := credencial.IdentidadeAutenticada(estado); err == nil {
				t.Fatal("deveria falhar")
			} else if !falha.TemCategoria(err, falha.CategoriaInterna) {
				t.Errorf("categoria = %v, esperado CategoriaInterna", falha.CategoriaDe(err))
			}
		})
	}
}

// TestMaterialDistingueDesligadoDeIncompleto protege contra o desastre silencioso.
//
// Quem informa a CA e esquece a chave ESPERA estar com autenticacao ligada. Subir sem
// ela produziria um gateway que aceita qualquer origem, com o operador convencido do
// contrario — pior que qualquer um dos dois extremos.
func TestMaterialDistingueDesligadoDeIncompleto(t *testing.T) {
	nenhum := credencial.Material{}
	if nenhum.Algum() || nenhum.Completo() {
		t.Error("material vazio deveria ser reconhecido como desligado")
	}

	parcial := credencial.Material{CaminhoDaCA: "ca.crt"}
	if !parcial.Algum() {
		t.Error("material parcial deveria ser reconhecido como tentativa de ligar")
	}
	if parcial.Completo() {
		t.Error("material parcial nao deveria passar por completo")
	}

	completo := credencial.Material{
		CaminhoDaCA: "ca.crt", CaminhoDoCertificado: "x.crt", CaminhoDaChave: "x.key",
	}
	if !completo.Completo() {
		t.Error("material completo deveria passar")
	}
}
