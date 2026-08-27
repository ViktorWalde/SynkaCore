package apresentacaohttp

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/ViktorWalde/SynkaCore/internal/dominio/instalacao"
)

// CaminhoDoEsboco devolve um YAML de configuracao pronto para editar.
const CaminhoDoEsboco = CaminhoDeComissionamento + "/esboco"

// responderEsboco gera a configuracao da instalacao a partir do que as origens ja
// declararam.
//
// Existe porque digitar dispositivo, modulo, canal, grandeza e unidade a mao, para
// dezenas de canais, e ONDE O ERRO DE COMISSIONAMENTO NASCE — e e justamente o erro
// que o relatorio de comissionamento existe para pegar depois. Gerar o esqueleto a
// partir do que as origens de fato enviam elimina a classe inteira: o que o gateway
// escreve aqui casa com o fio por construcao.
//
// O que ele NAO preenche e o nome do ponto de medicao, e a omissao e deliberada: so
// uma pessoa sabe que o canal 0 e o mancal da prensa da linha 2. Inventar um nome
// como "camara-01.canal-0" produziria uma configuracao que carrega, funciona, e nao
// diz nada — e um nome ruim que ja funciona nunca e corrigido.
//
// Devolve texto puro, e nao JSON, porque o destino do conteudo e um arquivo:
// `curl ... > configuracao/instalacao.yaml` precisa produzir algo valido.
func (a *Apresentacao) responderEsboco(escritor http.ResponseWriter, _ *http.Request) {
	a.declaracoes.mutex.RLock()
	declaracoes := make(map[string]declaracaoDaOrigem, len(a.declaracoes.porDispositivo))
	for nome, declaracao := range a.declaracoes.porDispositivo {
		declaracoes[nome] = declaracao
	}
	a.declaracoes.mutex.RUnlock()

	escritor.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	escritor.WriteHeader(http.StatusOK)

	if _, err := escritor.Write([]byte(a.montarEsboco(declaracoes))); err != nil {
		a.registro.Debug("esboco nao chegou ao chamador", "erro", err.Error())
	}
}

// montarEsboco escreve o YAML. Separado do handler para ser testavel sem HTTP.
func (a *Apresentacao) montarEsboco(declaracoes map[string]declaracaoDaOrigem) string {
	var esboco strings.Builder

	esboco.WriteString("# Esboco de configuracao gerado pelo SynkaCore.\n#\n")
	esboco.WriteString("# Montado a partir dos descritores que as origens ja enviaram, entao\n")
	esboco.WriteString("# dispositivo, modulo, canal, grandeza e unidade casam com o fio por\n")
	esboco.WriteString("# construcao — e nao dependem de ninguem digitar certo.\n#\n")
	esboco.WriteString("# FALTA VOCE: substituir cada `ponto:` marcado com AJUSTAR pelo nome real do\n")
	esboco.WriteString("# ponto de medicao. So uma pessoa sabe que o canal 0 e o mancal da prensa da\n")
	esboco.WriteString("# linha 2, e um nome inventado que ja funciona nunca e corrigido.\n#\n")
	esboco.WriteString("# Use hierarquia com ponto: \"linha-2.prensa-01.temperatura-mancal\" permite\n")
	esboco.WriteString("# consultar uma linha inteira depois; \"temp1\" nao permite nada.\n#\n")
	esboco.WriteString("#     curl http://127.0.0.1:8080" + CaminhoDoEsboco + " > configuracao/instalacao.yaml\n\n")

	if len(declaracoes) == 0 {
		// Um esboco vazio sem explicacao pareceria defeito do gateway. O motivo real
		// e quase sempre "nenhuma origem se apresentou ainda", que se resolve
		// esperando — e dizer isso e mais util que devolver um arquivo em branco.
		esboco.WriteString("# NENHUMA ORIGEM SE APRESENTOU AINDA.\n#\n")
		esboco.WriteString("# As origens enviam o descritor no boot e a cada 5 minutos. Se o gateway\n")
		esboco.WriteString("# acabou de subir, espere o proximo envio e consulte de novo.\n")
		return esboco.String()
	}

	nomeDaInstalacao := "AJUSTAR-nome-da-instalacao"
	if a.configuracao != nil {
		nomeDaInstalacao = a.configuracao.ID()
	}
	esboco.WriteString("instalacao: " + nomeDaInstalacao + "\n\n")
	esboco.WriteString("pontos_de_medicao:\n")

	// Ordem estavel: o esboco e regerado e comparado com o arquivo em uso, e uma
	// ordem que muda a cada consulta tornaria esse diff ilegivel.
	dispositivos := make([]string, 0, len(declaracoes))
	for nome := range declaracoes {
		dispositivos = append(dispositivos, nome)
	}
	sort.Strings(dispositivos)

	for _, dispositivo := range dispositivos {
		canais := append([]instalacao.CanalDeclarado(nil), declaracoes[dispositivo].canais...)
		sort.Slice(canais, func(primeiro, segundo int) bool {
			return canais[primeiro].Endereco.String() < canais[segundo].Endereco.String()
		})

		for _, canal := range canais {
			a.escreverCanal(&esboco, dispositivo, canal)
		}
	}

	return esboco.String()
}

// escreverCanal emite a entrada de um canal no esboco.
func (a *Apresentacao) escreverCanal(esboco *strings.Builder, dispositivo string,
	canal instalacao.CanalDeclarado) {

	// Se o canal JA esta configurado, o esboco preserva o nome do ponto em vez de
	// marcar AJUSTAR. Assim regerar o esboco depois de uma configuracao parcial nao
	// apaga o trabalho ja feito — o que transformaria a ferramenta numa armadilha.
	ponto := "AJUSTAR-" + dispositivo + "-canal-" + strconv.FormatUint(uint64(canal.Endereco.IndiceDoCanal), 10)
	if a.configuracao != nil {
		chave := instalacao.ChaveDeCanal{Endereco: canal.Endereco}
		if configurado, existe := a.resolverParaEsboco(dispositivo, chave); existe {
			ponto = configurado.Ponto.String()
		}
	}

	grandeza := instalacao.NomeDaGrandeza(canal.Grandeza)
	if canal.Grandeza == 0 {
		// Origem que nao declara grandeza deixa a decisao para quem configura. Um
		// palpite aqui seria pior que a marca: ele carregaria sem reclamar.
		grandeza = "AJUSTAR-veja-" + "/contrato"
	}
	unidade := canal.Unidade
	if strings.TrimSpace(unidade) == "" {
		unidade = "AJUSTAR-unidade-UCUM"
	}

	esboco.WriteString("  - dispositivo: " + dispositivo + "\n")
	esboco.WriteString("    modulo: " + strconv.FormatUint(uint64(canal.Endereco.IndiceDoModulo), 10) + "\n")
	esboco.WriteString("    canal: " + strconv.FormatUint(uint64(canal.Endereco.IndiceDoCanal), 10) + "\n")
	esboco.WriteString("    ponto: " + ponto + "\n")
	esboco.WriteString("    grandeza: " + grandeza + "\n")
	esboco.WriteString("    unidade: \"" + unidade + "\"\n")
	esboco.WriteString("    # faixa_minima e faixa_maxima sao opcionais: descrevem o INSTRUMENTO,\n")
	esboco.WriteString("    # nao o processo. Leitura fora delas e marcada, nunca recusada.\n\n")
}

// resolverParaEsboco busca a configuracao vigente de um canal, se houver.
func (a *Apresentacao) resolverParaEsboco(dispositivo string,
	chave instalacao.ChaveDeCanal) (instalacao.PontoConfigurado, bool) {

	for _, canal := range a.configuracao.CanaisConfigurados() {
		if canal.Dispositivo.String() != dispositivo || canal.Endereco != chave.Endereco {
			continue
		}
		return a.configuracao.Resolver(canal, a.relogio.Agora())
	}
	return instalacao.PontoConfigurado{}, false
}
