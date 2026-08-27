// Command synkacore-credencial emite a CA interna e os certificados da instalacao.
//
// POR QUE ISTO EXISTE. A identidade de um dispositivo precisa ser PROVADA, nao
// apenas afirmada. Sem isso, um dispositivo legitimo pode enviar dados se passando
// por outro — e o dado resultante e plausivel, atribuido ao equipamento errado, sem
// nada acusar. E o achado classificado como critico na revisao arquitetural.
//
// A credencial e POR DISPOSITIVO e nunca compartilhada. Comprometer um no de um
// armario destrancado compromete AQUELE no, nao a frota.
//
// ESTE E UM ATALHO MANUAL, e esta registrado como tal. O objetivo declarado do
// comissionamento e que o tecnico do cliente instale "sem digitar chave e sem editar
// arquivo de configuracao". Emitir certificado por linha de comando e exatamente
// digitar chave. Fica marcado para automatizar antes de soltar para cliente —
// atalho que fica implicito vira permanente.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/identidadededispositivo"
)

const (
	// validadeDaCA cobre a vida util esperada de uma instalacao.
	//
	// Vinte anos porque trocar a CA e operacao de campo em TODOS os nos ao mesmo
	// tempo: cada um precisa receber a nova ancora de confianca. Numa planta sem
	// internet, atualizada pelo notebook de um tecnico, isso e a operacao mais cara
	// que existe. Ela nao pode ser forcada por vencimento.
	validadeDaCA = 20 * 365 * 24 * time.Hour

	// validadeDoDispositivo e deliberadamente longa, e o custo esta declarado abaixo.
	//
	// Certificado que vence e no que PARA DE FUNCIONAR — e para numa planta, sem
	// aviso, no dia do vencimento. Sem infraestrutura de rotacao automatica (que uma
	// instalacao offline nao tem), validade curta troca um risco de seguranca por um
	// risco de disponibilidade, e disponibilidade e o que este sistema existe para
	// entregar.
	//
	// CONTRAPARTIDA HONESTA: nao ha revogacao. Um dispositivo comprometido continua
	// aceito ate a validade acabar ou ate a CA ser trocada. Ver docs/V2.1.md.
	validadeDoDispositivo = 10 * 365 * 24 * time.Hour

	validadeDoGateway = 10 * 365 * 24 * time.Hour

	// permissaoDeChave restringe a chave privada ao dono.
	//
	// Chave privada legivel por outros usuarios e o mesmo que nao ter chave privada.
	permissaoDeChave = 0o600

	permissaoDeCertificado = 0o644
	permissaoDePasta       = 0o750
)

func main() {
	if len(os.Args) < 2 {
		usar()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "ca":
		err = comandoCA(os.Args[2:])
	case "gateway":
		err = comandoGateway(os.Args[2:])
	case "dispositivo":
		err = comandoDispositivo(os.Args[2:])
	default:
		usar()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "synkacore-credencial: %v\n", err)
		os.Exit(1)
	}
}

func usar() {
	fmt.Fprint(os.Stderr, `synkacore-credencial — emite a CA interna e os certificados da instalacao.

  ca           cria a autoridade certificadora da instalacao
  gateway      emite o certificado de servidor do gateway
  dispositivo  emite o certificado de um no

Ordem: primeiro a CA, depois o gateway, depois um por dispositivo.

Exemplo:
  synkacore-credencial ca -pasta credenciais -instalacao planta-piloto
  synkacore-credencial gateway -pasta credenciais -endereco 192.168.0.100
  synkacore-credencial dispositivo -pasta credenciais -id esp32-sala-01
`)
}

// comandoCA cria a autoridade certificadora da instalacao.
func comandoCA(argumentos []string) error {
	conjunto := flag.NewFlagSet("ca", flag.ExitOnError)
	pasta := conjunto.String("pasta", "credenciais", "pasta onde gravar as credenciais")
	instalacao := conjunto.String("instalacao", "", "identificador da instalacao (obrigatorio)")
	if err := conjunto.Parse(argumentos); err != nil {
		return err
	}
	if *instalacao == "" {
		return fmt.Errorf("informe -instalacao")
	}

	if err := os.MkdirAll(*pasta, permissaoDePasta); err != nil {
		return fmt.Errorf("nao foi possivel criar a pasta: %w", err)
	}

	caminhoDoCertificado := filepath.Join(*pasta, "ca.crt")
	if _, err := os.Stat(caminhoDoCertificado); err == nil {
		// Recusa sobrescrever, e a recusa e o comportamento importante.
		//
		// Regerar a CA INVALIDA todos os certificados ja emitidos: a frota inteira
		// para de ser aceita de uma vez. Um comando que faz isso em silencio, por
		// engano, e a pior ferramenta possivel numa planta.
		return fmt.Errorf("ja existe uma CA em %s. "+
			"Regerar invalidaria TODOS os certificados ja emitidos e derrubaria a frota inteira. "+
			"Se e mesmo o que voce quer, apague a pasta a mao", caminhoDoCertificado)
	}

	chave, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("geracao da chave da CA falhou: %w", err)
	}

	serie, err := sortearSerie()
	if err != nil {
		return err
	}

	agora := time.Now()
	modelo := x509.Certificate{
		SerialNumber: serie,
		Subject: pkix.Name{
			CommonName:   "SynkaCore CA — " + *instalacao,
			Organization: []string{"SynkaCore"},
		},
		NotBefore: agora.Add(-time.Hour), // folga para desvio de relogio entre maquinas
		NotAfter:  agora.Add(validadeDaCA),

		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,

		// Sem CA intermediaria. Uma instalacao tem uma unica autoridade, e permitir
		// intermediarias criaria um caminho de confianca que ninguem pretende usar e
		// que ninguem vai auditar.
		MaxPathLen:     0,
		MaxPathLenZero: true,
	}

	certificado, err := x509.CreateCertificate(rand.Reader, &modelo, &modelo, &chave.PublicKey, chave)
	if err != nil {
		return fmt.Errorf("emissao da CA falhou: %w", err)
	}

	if err := gravarCertificado(caminhoDoCertificado, certificado); err != nil {
		return err
	}
	if err := gravarChave(filepath.Join(*pasta, "ca.key"), chave); err != nil {
		return err
	}

	fmt.Printf("CA criada para a instalacao %q, valida ate %s\n",
		*instalacao, modelo.NotAfter.Format("2006-01-02"))
	fmt.Printf("  %s\n  %s  (GUARDE ESTA CHAVE: quem a tem emite qualquer identidade)\n",
		caminhoDoCertificado, filepath.Join(*pasta, "ca.key"))
	return nil
}

// comandoGateway emite o certificado de servidor do gateway.
func comandoGateway(argumentos []string) error {
	conjunto := flag.NewFlagSet("gateway", flag.ExitOnError)
	pasta := conjunto.String("pasta", "credenciais", "pasta das credenciais")
	enderecos := conjunto.String("endereco", "",
		"IPs e nomes pelos quais os nos alcancam o gateway, separados por virgula")
	if err := conjunto.Parse(argumentos); err != nil {
		return err
	}
	if *enderecos == "" {
		return fmt.Errorf("informe -endereco com o IP que os nos usam para alcancar o gateway")
	}

	ca, chaveDaCA, err := carregarCA(*pasta)
	if err != nil {
		return err
	}

	chave, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("geracao da chave do gateway falhou: %w", err)
	}

	serie, err := sortearSerie()
	if err != nil {
		return err
	}

	agora := time.Now()
	modelo := x509.Certificate{
		SerialNumber: serie,
		Subject: pkix.Name{
			CommonName:   "synkacore-gateway",
			Organization: []string{"SynkaCore"},
		},
		NotBefore:   agora.Add(-time.Hour),
		NotAfter:    agora.Add(validadeDoGateway),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	// Os enderecos entram como SAN. Sem eles, o no recusa a conexao mesmo com o
	// certificado assinado pela CA certa: validar servidor e confirmar que o
	// certificado corresponde ao ENDERECO discado, e nao apenas que ele e valido.
	for _, endereco := range separarPorVirgula(*enderecos) {
		if ip := net.ParseIP(endereco); ip != nil {
			modelo.IPAddresses = append(modelo.IPAddresses, ip)
			continue
		}
		modelo.DNSNames = append(modelo.DNSNames, endereco)
	}

	certificado, err := x509.CreateCertificate(rand.Reader, &modelo, ca, &chave.PublicKey, chaveDaCA)
	if err != nil {
		return fmt.Errorf("emissao do certificado do gateway falhou: %w", err)
	}

	if err := gravarCertificado(filepath.Join(*pasta, "gateway.crt"), certificado); err != nil {
		return err
	}
	if err := gravarChave(filepath.Join(*pasta, "gateway.key"), chave); err != nil {
		return err
	}

	fmt.Printf("certificado do gateway emitido para %s, valido ate %s\n",
		*enderecos, modelo.NotAfter.Format("2006-01-02"))
	return nil
}

// comandoDispositivo emite o certificado de um no.
func comandoDispositivo(argumentos []string) error {
	conjunto := flag.NewFlagSet("dispositivo", flag.ExitOnError)
	pasta := conjunto.String("pasta", "credenciais", "pasta das credenciais")
	identificador := conjunto.String("id", "", "identificador do dispositivo (obrigatorio)")
	if err := conjunto.Parse(argumentos); err != nil {
		return err
	}
	if *identificador == "" {
		return fmt.Errorf("informe -id")
	}

	// Validado com a MESMA funcao que o gateway usa ao receber a remessa.
	//
	// Sem isso, seria possivel emitir um certificado para um identificador que o
	// gateway depois recusa — e o no falharia no primeiro despacho, em campo, com
	// erro que nao aponta para o certificado.
	dispositivo, err := identidadededispositivo.AnalisarIDDoDispositivo(*identificador)
	if err != nil {
		return fmt.Errorf("identificador de dispositivo invalido: %w", err)
	}

	ca, chaveDaCA, err := carregarCA(*pasta)
	if err != nil {
		return err
	}

	chave, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("geracao da chave do dispositivo falhou: %w", err)
	}

	serie, err := sortearSerie()
	if err != nil {
		return err
	}

	agora := time.Now()
	modelo := x509.Certificate{
		SerialNumber: serie,

		// O NOME COMUM E O IDENTIFICADOR DO DISPOSITIVO. Esta linha e o ponto
		// inteiro do certificado de cliente: e ela que o gateway compara com a
		// identidade REIVINDICADA na remessa.
		Subject: pkix.Name{
			CommonName:   dispositivo.String(),
			Organization: []string{"SynkaCore"},
		},

		NotBefore:   agora.Add(-time.Hour),
		NotAfter:    agora.Add(validadeDoDispositivo),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	certificado, err := x509.CreateCertificate(rand.Reader, &modelo, ca, &chave.PublicKey, chaveDaCA)
	if err != nil {
		return fmt.Errorf("emissao do certificado do dispositivo falhou: %w", err)
	}

	pastaDosDispositivos := filepath.Join(*pasta, "dispositivos")
	if err := os.MkdirAll(pastaDosDispositivos, permissaoDePasta); err != nil {
		return fmt.Errorf("nao foi possivel criar a pasta de dispositivos: %w", err)
	}

	base := filepath.Join(pastaDosDispositivos, dispositivo.String())
	if err := gravarCertificado(base+".crt", certificado); err != nil {
		return err
	}
	if err := gravarChave(base+".key", chave); err != nil {
		return err
	}

	fmt.Printf("certificado emitido para o dispositivo %q, valido ate %s\n",
		dispositivo, modelo.NotAfter.Format("2006-01-02"))
	fmt.Printf("  copie para o no: %s.crt, %s.key e %s\n",
		base, base, filepath.Join(*pasta, "ca.crt"))
	return nil
}

// carregarCA le a autoridade certificadora da pasta.
func carregarCA(pasta string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	brutoDoCertificado, err := os.ReadFile(filepath.Join(pasta, "ca.crt")) //nolint:gosec // caminho do operador
	if err != nil {
		return nil, nil, fmt.Errorf("CA nao encontrada: rode primeiro o comando `ca`: %w", err)
	}
	brutoDaChave, err := os.ReadFile(filepath.Join(pasta, "ca.key")) //nolint:gosec // caminho do operador
	if err != nil {
		return nil, nil, fmt.Errorf("chave da CA nao encontrada: %w", err)
	}

	blocoDoCertificado, _ := pem.Decode(brutoDoCertificado)
	if blocoDoCertificado == nil {
		return nil, nil, fmt.Errorf("ca.crt nao e PEM valido")
	}
	certificado, err := x509.ParseCertificate(blocoDoCertificado.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("ca.crt ilegivel: %w", err)
	}

	blocoDaChave, _ := pem.Decode(brutoDaChave)
	if blocoDaChave == nil {
		return nil, nil, fmt.Errorf("ca.key nao e PEM valido")
	}
	chave, err := x509.ParseECPrivateKey(blocoDaChave.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("ca.key ilegivel: %w", err)
	}

	return certificado, chave, nil
}

// sortearSerie gera o numero de serie do certificado.
//
// Sorteado com 128 bits, e nao sequencial: numero de serie previsivel facilita
// ataques de colisao contra a assinatura, e um contador exigiria estado persistente
// que a ferramenta nao tem.
func sortearSerie() (*big.Int, error) {
	limite := new(big.Int).Lsh(big.NewInt(1), 128)
	serie, err := rand.Int(rand.Reader, limite)
	if err != nil {
		return nil, fmt.Errorf("sorteio do numero de serie falhou: %w", err)
	}
	return serie, nil
}

func gravarCertificado(caminho string, bruto []byte) error {
	codificado := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: bruto})
	if err := os.WriteFile(caminho, codificado, permissaoDeCertificado); err != nil {
		return fmt.Errorf("nao foi possivel gravar %s: %w", caminho, err)
	}
	return nil
}

func gravarChave(caminho string, chave *ecdsa.PrivateKey) error {
	bruto, err := x509.MarshalECPrivateKey(chave)
	if err != nil {
		return fmt.Errorf("serializacao da chave falhou: %w", err)
	}
	codificado := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: bruto})
	if err := os.WriteFile(caminho, codificado, permissaoDeChave); err != nil {
		return fmt.Errorf("nao foi possivel gravar %s: %w", caminho, err)
	}
	return nil
}

// separarPorVirgula divide a lista de enderecos, descartando entradas vazias.
func separarPorVirgula(texto string) []string {
	var partes []string
	for _, parte := range strings.Split(texto, ",") {
		if aparada := strings.TrimSpace(parte); aparada != "" {
			partes = append(partes, aparada)
		}
	}
	return partes
}
