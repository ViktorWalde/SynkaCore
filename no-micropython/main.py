"""No SynkaCore em MicroPython — ESP32 com sensor DHT11.

Este arquivo e o equivalente do `synkacore-no` em Go, e obedece as MESMAS regras.
Ele fala com o gateway pelo mesmo contrato de fio, e o gateway nao tem como saber
que do outro lado ha um ESP32 e nao o simulador.

DUAS REGRAS QUE VIERAM DO NO EM GO, E O MOTIVO DELAS:

1. A AMOSTRAGEM TEM PERIODO FIXO E NUNCA ESPERA A REDE.
   O laco de despacho pode recuar por segundos quando o gateway cai; se a
   amostragem esperasse por ele, a serie ganharia buracos permanentes. Dado que
   nao foi MEDIDO nao esta em buffer nenhum, e nenhuma retransmissao o traz de
   volta. Aqui isso e garantido pelo relogio: cada ciclo mede quando o proximo
   instante de amostragem chega, independente do que o despacho esteja fazendo.

2. O NO NUNCA AFIRMA SABER A HORA.
   O ESP32 nao tem relogio com bateria — ao ligar, ele comeca em 1970. Ele reporta
   apenas `time.ticks_ms()`, que e monotonico, e sorteia uma sessao de boot a cada
   partida. O gateway ancora esse tempo ao relogio dele.

LIMITES REAIS DO DHT11, e por que eles aparecem no codigo:

    taxa maxima   1 Hz; o datasheet recomenda >= 5 s para leitura precisa
    resolucao     1 grau C e 1 % UR — inteiros, nao existe 24,3 grau
    faixa         0 a 50 grau C, 20 a 90 % UR
    exatidao      +-2 grau C, +-5 % UR

A faixa vai na configuracao da instalacao como `faixa_minima`/`faixa_maxima`: ela
descreve o INSTRUMENTO, e leitura fora dela e marcada, nunca recusada.
"""

import gc
import time
import urandom

import machine
import network
import dht
import socket

import synkacore_contrato as contrato

# ---------------------------------------------------------------------------
# CONFIGURACAO
# ---------------------------------------------------------------------------

REDE_SSID = "AJUSTE"
REDE_SENHA = "AJUSTE"

# Endereco do gateway na rede de chao de fabrica.
GATEWAY_HOST = "192.168.0.100"
GATEWAY_PORTA = 8443
GATEWAY_CAMINHO = "/ingestao"

ID_DA_INSTALACAO = "planta-piloto"

# Unico na instalacao INTEIRA, nunca so dentro de um gateway: se dois gateways
# numerassem dispositivos a partir de 1, juntar os dados depois seria irreversivel.
ID_DO_DISPOSITIVO = "esp32-sala-01"

PINO_DO_DHT11 = 4

# Cinco segundos, e nao um. O DHT11 aceita 1 Hz, mas o datasheet recomenda >= 5 s
# para leitura precisa — abaixo disso o proprio sensor se aquece e a medida deriva.
# Amostrar mais rapido produziria mais numeros e menos informacao.
INTERVALO_DE_AMOSTRAGEM_MS = 5_000

# Lote. Nao e otimizacao: sem ele, o fsync do gateway sozinho consome a capacidade
# do disco, e uma requisicao HTTPS por amostra e inviavel num ESP32.
ENVELOPES_POR_REMESSA = 24

# Prazo maximo que uma amostra pode esperar no buffer antes de ser despachada.
#
# E o orcamento de latencia que o contrato declara para ClasseDeDado.AMOSTRA, e ele
# precisa estar aqui porque o lote sozinho nao basta: com amostragem a 5 s e dois
# envelopes por ciclo, encher um lote de 24 levaria um minuto — doze vezes o
# orcamento. O gatilho por tempo e o que impede a telemetria de envelhecer no buffer
# so porque o lote nao encheu.
LATENCIA_MAXIMA_DE_AMOSTRA_MS = 5_000

# Cinco minutos entre autodeclaracoes. Baixa frequencia de proposito: assim a
# descricao do canal nao viaja em cada amostra.
INTERVALO_DO_DESCRITOR_MS = 300_000
INTERVALO_DE_SAUDE_MS = 60_000

# Autonomia do buffer local. Com amostragem a 5 s e tres envelopes por ciclo,
# 600 itens cobrem cerca de 15 minutos de gateway fora — folgado para reinicio do
# gateway e queda curta de rede.
CAPACIDADE_DO_BUFFER = 600

RECUO_BASE_MS = 1_000
RECUO_TETO_MS = 30_000

# Canais que este no expoe. Sao o contrato entre o firmware e a configuracao da
# instalacao no gateway.
CANAL_DE_TEMPERATURA = 0
CANAL_DE_UMIDADE = 1

VERSAO_DO_ESQUEMA = 1


def sortear_sessao_de_boot():
    """Sorteia o identificador desta partida.

    Sorteado a cada boot, e nunca configurado. E o que permite ao gateway ancorar o
    tempo monotonico desta execucao, e o que impede os numeros de sequencia — que
    reiniciam em zero agora — de colidirem com os da partida anterior. Fixa-lo faria
    a segunda partida ter todo o seu dado descartado como duplicata.
    """
    return "boot-" + "".join("%02x" % urandom.getrandbits(8) for _ in range(8))


class No:
    def __init__(self):
        self.sessao_de_boot = sortear_sessao_de_boot()
        self.sensor = dht.DHT11(machine.Pin(PINO_DO_DHT11))
        self.sequencia = 0
        self.buffer = []
        self.descartados = 0
        self.tentativas_seguidas = 0
        self.partida_ms = time.ticks_ms()

    # -- tempo -------------------------------------------------------------

    def tempo_ligado_ms(self):
        """Tempo monotonico desde a partida. O UNICO tempo que este no afirma.

        ticks_diff trata o retorno a zero do contador, que acontece a cada ~12 dias
        num ESP32. Sem ele, o tempo ligado daria um salto negativo e o gateway
        recusaria a serie inteira daquela sessao.
        """
        return time.ticks_diff(time.ticks_ms(), self.partida_ms)

    # -- buffer ------------------------------------------------------------

    def enfileirar(self, conteudo_nome, conteudo):
        """Carimba o envelope e guarda no buffer.

        A sequencia e o tempo ligado sao atribuidos AQUI, num lugar so. Atribui-los
        em cada chamador abriria a porta para dois envelopes com o mesmo numero, e
        numeros repetidos na mesma sessao fariam o gateway descartar um deles como
        duplicata.
        """
        self.sequencia += 1
        envelope = {
            "numero_de_sequencia": self.sequencia,
            "tempo_ligado_ms": self.tempo_ligado_ms(),
            conteudo_nome: conteudo,
        }

        if len(self.buffer) >= CAPACIDADE_DO_BUFFER:
            # Descarta o mais antigo e CONTABILIZA. Perda aceita continua sendo perda
            # conhecida, em numeros, e ela viaja na telemetria de saude.
            self.buffer.pop(0)
            self.descartados += 1

        self.buffer.append(envelope)

    # -- amostragem --------------------------------------------------------

    def amostrar(self):
        """Le o DHT11 e enfileira temperatura e umidade."""
        try:
            self.sensor.measure()
        except OSError as erro:
            # Falha de leitura NAO derruba o laco. O DHT11 falha esporadicamente por
            # ruido no barramento de um fio, e a proxima leitura costuma passar.
            # Parar o no por isso seria transformar uma falha transitoria em queda.
            print("dht: leitura falhou:", erro)
            return

        for canal, valor in (
            (CANAL_DE_TEMPERATURA, self.sensor.temperature()),
            (CANAL_DE_UMIDADE, self.sensor.humidity()),
        ):
            self.enfileirar("amostra_escalar", {
                "endereco": {"indice_do_modulo": 0, "indice_do_canal": canal},
                # float() explicito: o DHT11 devolve INTEIRO, e o contrato declara
                # float32. Deixar o inteiro passar faria o codificador emitir varint
                # onde o gateway espera fixed32, e o quadro seria recusado.
                "valor": float(valor),
            })

    def emitir_saude(self):
        """Telemetria interna do no.

        Os dois campos de memoria viajam SEPARADOS de proposito. O risco real num
        ESP32 nao e o vazamento classico, e a FRAGMENTACAO: o total livre continua
        alto enquanto o maior bloco contiguo encolhe, ate a proxima alocacao de TLS
        falhar — tipicamente depois de dias. So o total esconderia exatamente a falha
        que mata o no em campo.
        """
        livre = gc.mem_free()
        maior_bloco = livre

        try:
            import esp32
            # idf_heap_info devolve tuplas (total, livre, maior bloco, minimo visto).
            # O maior bloco e o terceiro elemento, e e o que denuncia fragmentacao.
            regioes = esp32.idf_heap_info(esp32.HEAP_DATA)
            if regioes:
                livre = sum(regiao[1] for regiao in regioes)
                maior_bloco = max(regiao[2] for regiao in regioes)
        except (ImportError, AttributeError):
            # Sem a API do IDF, o no reporta o que ele PODE afirmar de verdade.
            # Inventar um numero para o maior bloco seria pior que nao ter: alguem
            # confiaria num indicador falso de fragmentacao.
            pass

        self.enfileirar("saude_da_origem", {
            "bytes_livres_de_memoria": livre,
            "maior_bloco_livre_em_bytes": maior_bloco,
            "registros_descartados": self.descartados,
            "bytes_usados_no_buffer": len(self.buffer) * 32,
        })

    def emitir_descritor(self):
        """Autodeclaracao: o que este no acredita medir em cada canal.

        Rede de protecao de comissionamento: o gateway compara isto com o mapeamento
        que ele mesmo tem e denuncia divergencia. Canal trocado no painel deixa de
        ser erro silencioso.
        """
        self.enfileirar("descritor_da_origem", {
            "versao_do_firmware": "synkacore-no-micropython/2.0",
            "modelo_do_hardware": "esp32/dht11",
            "canais": [
                {
                    "endereco": {"indice_do_modulo": 0, "indice_do_canal": CANAL_DE_TEMPERATURA},
                    "grandeza": contrato.GRANDEZA_TEMPERATURA,
                    "unidade": "Cel",
                    "periodo_de_amostragem_ms": INTERVALO_DE_AMOSTRAGEM_MS,
                },
                {
                    "endereco": {"indice_do_modulo": 0, "indice_do_canal": CANAL_DE_UMIDADE},
                    "grandeza": contrato.GRANDEZA_UMIDADE_DO_AR,
                    "unidade": "%",
                    "periodo_de_amostragem_ms": INTERVALO_DE_AMOSTRAGEM_MS,
                },
            ],
        })

    # -- despacho ----------------------------------------------------------

    def despachar(self):
        """Entrega um lote ao gateway. Devolve True se foi confirmado."""
        if not self.buffer:
            return True

        lote = self.buffer[:ENVELOPES_POR_REMESSA]
        corpo = contrato.codificar_Remessa({
            "versao_do_esquema": VERSAO_DO_ESQUEMA,
            "id_da_instalacao": ID_DA_INSTALACAO,
            "id_do_dispositivo": ID_DO_DISPOSITIVO,
            "id_da_sessao_de_boot": self.sessao_de_boot,
            "envelopes": lote,
        })

        try:
            confirmacao = self.enviar(corpo)
        except OSError as erro:
            # O lote PERMANECE no buffer. So sai depois de confirmado — e essa e a
            # propriedade que garante que nada se perde quando o gateway cai.
            print("despacho falhou:", erro)
            self.tentativas_seguidas += 1
            time.sleep_ms(self.recuo_ms())
            return False

        self.tentativas_seguidas = 0
        duravel_ate = confirmacao.get("duravel_ate_a_sequencia", 0)

        # Libera do buffer o que ficou DURAVEL, e apenas isso.
        self.buffer = [e for e in self.buffer if e["numero_de_sequencia"] > duravel_ate]

        rejeitadas = confirmacao.get("sequencias_rejeitadas_definitivamente", [])
        if rejeitadas:
            # Rejeicao definitiva NAO volta ao buffer: retransmitir conteudo que o
            # gateway nunca vai aceitar faria o no tentar para sempre, e o buffer
            # encheria de dado condenado empurrando dado bom para fora.
            print("rejeitados definitivamente:", rejeitadas)
            self.buffer = [e for e in self.buffer
                           if e["numero_de_sequencia"] not in rejeitadas]
        return True

    def recuo_ms(self):
        """Recuo exponencial com jitter, limitado por um teto.

        O jitter nao e refinamento: sem ele, todos os nos que falharem ao mesmo tempo
        tentam de novo nos mesmos instantes, a frota inteira reconecta junta, e o
        gateway que estava apenas lento cai de vez.
        """
        espera = min(RECUO_BASE_MS * (2 ** min(self.tentativas_seguidas, 5)), RECUO_TETO_MS)
        return espera + urandom.getrandbits(8) * espera // 1024

    def enviar(self, corpo):
        """POST do lote, em HTTP puro.

        TEXTO CLARO POR ENQUANTO, e isto e uma divida declarada, nao um esquecimento.
        A V2.1 troca por mTLS com CA interna: o MicroPython suporta
        `ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)` com `load_cert_chain`, entao a troca
        e de transporte, nao de desenho. Ate la, este no so deve rodar em rede
        controlada.

        Requisicao montada a mao, sem urequests: e uma requisicao so, com corpo
        binario e cabecalho fixo — a biblioteca custaria memoria para nada.
        """
        endereco = socket.getaddrinfo(GATEWAY_HOST, GATEWAY_PORTA)[0][-1]
        conexao = socket.socket()
        conexao.settimeout(10)

        try:
            conexao.connect(endereco)
            cabecalho = (
                "POST %s HTTP/1.1\r\n"
                "Host: %s:%d\r\n"
                "Content-Type: application/x-protobuf\r\n"
                "Content-Length: %d\r\n"
                "Connection: close\r\n\r\n"
            ) % (GATEWAY_CAMINHO, GATEWAY_HOST, GATEWAY_PORTA, len(corpo))

            conexao.write(cabecalho.encode())
            conexao.write(corpo)

            resposta = b""
            while True:
                pedaco = conexao.read(256)
                if not pedaco:
                    break
                resposta += pedaco
        finally:
            conexao.close()

        separador = resposta.find(b"\r\n\r\n")
        if separador < 0:
            raise OSError("resposta sem corpo")

        linha_de_status = resposta[:resposta.find(b"\r\n")]
        if b"200" not in linha_de_status:
            # 4xx manda DESCARTAR, 5xx manda RETRANSMITIR. Aqui os dois viram
            # excecao e o lote fica no buffer; distingui-los e trabalho da V2.1,
            # junto com o transporte.
            raise OSError("gateway respondeu: %s" % linha_de_status)

        return contrato.decodificar_ConfirmacaoDeRemessa(resposta[separador + 4:])


def conectar_rede():
    """Sobe o Wi-Fi e espera associar."""
    interface = network.WLAN(network.STA_IF)
    interface.active(True)

    if not interface.isconnected():
        print("conectando a", REDE_SSID)
        interface.connect(REDE_SSID, REDE_SENHA)
        while not interface.isconnected():
            time.sleep_ms(200)

    print("rede:", interface.ifconfig())


def executar():
    conectar_rede()
    no = No()
    print("no iniciando; sessao de boot:", no.sessao_de_boot)

    no.emitir_descritor()

    proxima_amostra = time.ticks_ms()
    proximo_descritor = time.ticks_add(time.ticks_ms(), INTERVALO_DO_DESCRITOR_MS)
    proxima_saude = time.ticks_add(time.ticks_ms(), INTERVALO_DE_SAUDE_MS)
    ultimo_despacho = time.ticks_ms()

    while True:
        agora = time.ticks_ms()

        # A amostragem vem PRIMEIRO e e decidida pelo relogio, nao pelo despacho.
        # E o que garante periodo fixo mesmo com o gateway fora: o recuo do despacho
        # acontece depois, e a proxima amostra ja tem hora marcada.
        if time.ticks_diff(agora, proxima_amostra) >= 0:
            no.amostrar()
            proxima_amostra = time.ticks_add(proxima_amostra, INTERVALO_DE_AMOSTRAGEM_MS)

        if time.ticks_diff(agora, proxima_saude) >= 0:
            no.emitir_saude()
            proxima_saude = time.ticks_add(proxima_saude, INTERVALO_DE_SAUDE_MS)

        if time.ticks_diff(agora, proximo_descritor) >= 0:
            no.emitir_descritor()
            proximo_descritor = time.ticks_add(proximo_descritor, INTERVALO_DO_DESCRITOR_MS)

        # Dois gatilhos, e os dois sao necessarios: o lote cheio evita requisicao
        # grande demais, e o prazo evita que a amostra envelheca esperando o lote.
        loteCheio = len(no.buffer) >= ENVELOPES_POR_REMESSA
        prazoVencido = (no.buffer and
                        time.ticks_diff(agora, ultimo_despacho) >= LATENCIA_MAXIMA_DE_AMOSTRA_MS)

        if loteCheio or prazoVencido:
            if no.despachar():
                ultimo_despacho = time.ticks_ms()

        time.sleep_ms(100)


if __name__ == "__main__":
    executar()
