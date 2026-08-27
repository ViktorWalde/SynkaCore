// Package credencial resolve a identidade PROVADA de quem esta do outro lado da conexao.
//
// A distincao que este package existe para sustentar:
//
//	REIVINDICADA  — o id_do_dispositivo que vem dentro da remessa. Texto que
//	                qualquer um pode escrever.
//	AUTENTICADA   — o nome comum do certificado que o transporte validou contra a
//	                CA da instalacao. Prova criptografica.
//
// Sem confrontar as duas, um dispositivo legitimo pode enviar dados se passando por
// outro: ele tem certificado valido, entao o TLS aceita, e ele escreve na remessa o
// identificador do vizinho. O dado resultante e PLAUSIVEL e esta atribuido ao
// equipamento errado — e nada acusa, porque o gateway so viu uma conexao autenticada.
//
// Nao ha jeito de descobrir isso depois. E por isso que a confrontacao acontece na
// borda, antes de qualquer gravacao.
package credencial

import (
	"crypto/tls"
	"crypto/x509"
	"os"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
	"github.com/ViktorWalde/SynkaCore/internal/plataforma/falha"
)

const (
	operacaoCarregar   = "credencial.Carregar"
	operacaoAutenticar = "credencial.IdentidadeAutenticada"
	operacaoConferir   = "credencial.ConferirIdentidade"
)

// Material sao os arquivos de credencial de uma ponta.
type Material struct {
	// CaminhoDaCA e a ancora de confianca da instalacao.
	CaminhoDaCA string

	// CaminhoDoCertificado e CaminhoDaChave identificam esta ponta.
	CaminhoDoCertificado string
	CaminhoDaChave       string
}

// Completo informa se os tres caminhos foram fornecidos.
//
// Existe para que a raiz de composicao distinga "TLS desligado" de "TLS mal
// configurado": os tres juntos ligam, nenhum deixa desligado, e um subconjunto e
// erro — nunca um meio-termo silencioso.
func (m Material) Completo() bool {
	return m.CaminhoDaCA != "" && m.CaminhoDoCertificado != "" && m.CaminhoDaChave != ""
}

// Algum informa se pelo menos um caminho foi fornecido.
func (m Material) Algum() bool {
	return m.CaminhoDaCA != "" || m.CaminhoDoCertificado != "" || m.CaminhoDaChave != ""
}

// ConfiguracaoDeServidor monta o TLS do lado do gateway.
//
// ClientAuth e RequireAndVerifyClientCert, e nao um modo mais frouxo: o proposito
// inteiro e que so origens com credencial da instalacao consigam entregar dado.
// VerifyClientCertIfGiven aceitaria conexao sem certificado nenhum, o que
// transformaria a autenticacao em sugestao.
func ConfiguracaoDeServidor(material Material) (*tls.Config, error) {
	certificado, err := carregarParDeChaves(material)
	if err != nil {
		return nil, err
	}
	autoridade, err := carregarAutoridade(material.CaminhoDaCA)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{certificado},
		ClientCAs:    autoridade,
		ClientAuth:   tls.RequireAndVerifyClientCert,

		// TLS 1.2 e o piso porque e o que mbedTLS numa origem embarcada negocia com
		// folga. Abaixo disso ha suites quebradas; acima, nem todo firmware chega.
		MinVersion: tls.VersionTLS12,
	}, nil
}

// ConfiguracaoDeCliente monta o TLS do lado do no.
//
// A CA da instalacao entra como RootCAs, e nao se soma ao conjunto do sistema: o no
// deve confiar EXCLUSIVAMENTE na autoridade da planta. Herdar as autoridades
// publicas do sistema significaria aceitar qualquer certificado emitido por qualquer
// CA comercial — e numa rede de chao de fabrica isso nao protege de nada.
func ConfiguracaoDeCliente(material Material, nomeDoServidor string) (*tls.Config, error) {
	certificado, err := carregarParDeChaves(material)
	if err != nil {
		return nil, err
	}
	autoridade, err := carregarAutoridade(material.CaminhoDaCA)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{certificado},
		RootCAs:      autoridade,
		ServerName:   nomeDoServidor,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func carregarParDeChaves(material Material) (tls.Certificate, error) {
	certificado, err := tls.LoadX509KeyPair(material.CaminhoDoCertificado, material.CaminhoDaChave)
	if err != nil {
		return tls.Certificate{}, falha.Envolver(falha.CategoriaInterna, operacaoCarregar,
			"nao foi possivel carregar o certificado e a chave", err)
	}
	return certificado, nil
}

func carregarAutoridade(caminho string) (*x509.CertPool, error) {
	bruto, err := os.ReadFile(caminho) //nolint:gosec // caminho vem da configuracao do operador
	if err != nil {
		return nil, falha.Envolver(falha.CategoriaInterna, operacaoCarregar,
			"nao foi possivel ler a CA da instalacao em "+caminho, err)
	}

	// Conjunto NOVO, e nao x509.SystemCertPool(): a instalacao confia na propria
	// autoridade e em nenhuma outra.
	autoridade := x509.NewCertPool()
	if !autoridade.AppendCertsFromPEM(bruto) {
		return nil, falha.Nova(falha.CategoriaInterna, operacaoCarregar,
			"a CA em "+caminho+" nao contem nenhum certificado PEM valido")
	}
	return autoridade, nil
}

// IdentidadeAutenticada extrai o dispositivo PROVADO pelo certificado de cliente.
//
// O nome comum carrega o identificador, e a escolha e deliberada: ele e universalmente
// suportado por mbedTLS, que e o que roda numa origem embarcada. A depreciacao do nome
// comum vale para NOME DE MAQUINA em TLS de servidor — aqui ele identifica um
// dispositivo, e o alternativo (SAN de URI) so acrescentaria atrito no firmware.
//
// O identificador passa pela MESMA validacao que o gateway aplica ao recebe-lo na
// remessa. Um certificado emitido com identificador fora do alfabeto seria aceito
// pelo TLS e recusado pelo dominio — divergencia que apareceria como conexao que
// autentica e remessa que falha, sem apontar para o certificado.
func IdentidadeAutenticada(estado *tls.ConnectionState) (identidadededispositivo.IDDoDispositivo, error) {
	if estado == nil || len(estado.PeerCertificates) == 0 {
		// Alcancavel apenas com o servidor mal configurado: RequireAndVerifyClientCert
		// ja recusa a conexao antes do handler. Categoria interna porque, se chegou
		// aqui, o defeito e nosso.
		return identidadededispositivo.IDDoDispositivo{}, falha.Nova(falha.CategoriaInterna,
			operacaoAutenticar, "conexao sem certificado de cliente: verificacao do transporte nao rodou")
	}

	nomeComum := estado.PeerCertificates[0].Subject.CommonName
	if nomeComum == "" {
		return identidadededispositivo.IDDoDispositivo{}, falha.Nova(falha.CategoriaNaoAutenticado,
			operacaoAutenticar, "certificado de cliente sem nome comum: nao identifica dispositivo")
	}

	dispositivo, err := identidadededispositivo.AnalisarIDDoDispositivo(nomeComum)
	if err != nil {
		return identidadededispositivo.IDDoDispositivo{}, falha.Envolver(falha.CategoriaNaoAutenticado,
			operacaoAutenticar,
			"nome comum do certificado nao e um identificador de dispositivo valido: "+nomeComum, err)
	}
	return dispositivo, nil
}

// ConferirIdentidade recusa remessa cuja identidade reivindicada nao bate com a provada.
//
// ESTA E A TRAVA CENTRAL DA V2.1.
//
// Um dispositivo com certificado valido pode escrever na remessa o identificador de
// outro. O TLS aceita — a credencial e legitima. Sem esta conferencia, o dado seria
// gravado sob a identidade errada, plausivel e indetectavel: nao ha nada no registro
// que denuncie a troca depois.
//
// A categoria e PermissaoNegada, e nao NaoAutenticado, e a distincao importa: a
// credencial e valida, o portador simplesmente nao pode falar por aquele
// identificador. NaoAutenticado faria a origem tentar renovar credencial, que nao
// resolveria nada.
func ConferirIdentidade(
	autenticada identidadededispositivo.IDDoDispositivo,
	reivindicada string,
) error {
	if reivindicada == "" {
		return falha.Nova(falha.CategoriaEntradaInvalida, operacaoConferir,
			"remessa sem identificador de dispositivo")
	}
	if reivindicada != autenticada.String() {
		return falha.Nova(falha.CategoriaPermissaoNegada, operacaoConferir,
			"a remessa reivindica o dispositivo "+reivindicada+
				" mas o certificado prova "+autenticada.String())
	}
	return nil
}
