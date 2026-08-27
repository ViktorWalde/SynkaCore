# Nó SynkaCore em MicroPython — ESP32 + DHT11

Nó de aquisição rodando em ESP32 com MicroPython, medindo **temperatura e umidade do
ar** com um DHT11 e entregando ao gateway pelo contrato de fio do projeto.

> **Banco de testes.** O firmware de produção será C++ restrito sobre ESP-IDF —
> ver [`docs/NO-EMBARCADO.md`](../docs/NO-EMBARCADO.md). Este nó existe para provar que
> o caminho IoT funciona de ponta a ponta **antes** de alguém escrever firmware
> embarcado, que é a ponta mais cara de corrigir.

---

## O que ele prova

O gateway **não tem como saber** que do outro lado há um ESP32 com MicroPython e não o
simulador em Go. Os dois falam o mesmo contrato, e é isso que torna o desenvolvimento
sem hardware honesto em vez de conveniente:

| Exercitado de verdade | Como |
|---|---|
| Serialização protobuf | Codificador **gerado do `.proto`**, com fidelidade verificada byte a byte contra o Go |
| Lote e orçamento de latência | Despacho por lote cheio **ou** prazo vencido |
| Retransmissão | O lote só sai do buffer depois de confirmado |
| Contrapressão | Recuo exponencial com jitter |
| Idempotência | Sessão de boot sorteada + sequência dentro dela |
| Autoridade de tempo | O nó só reporta `ticks_ms`; nunca afirma saber a hora |

---

## Arquivos

| Arquivo | O que é |
|---|---|
| `synkacore_contrato.py` | **Gerado.** Codificador protobuf. Não edite — regere com `make no-micropython`. |
| `main.py` | O nó: amostragem, buffer, despacho. Escrito à mão. |

### Por que o codificador é gerado

O contrato `.proto` é a **fonte única** do sistema. Para o gateway, o `protoc` resolve.
Para MicroPython, nenhum gerador existente serve:

- **`uprotobuf`** gera do `.proto`, mas só fala **proto2** — sem `oneof` e sem
  `optional`, que é exatamente o que este contrato usa.
- **`minipb`** roda em MicroPython, mas o esquema é **escrito à mão** — e um esquema
  escrito à mão diverge do contrato sem nada acusar.

Escrever o codificador à mão funcionaria no primeiro dia e apodreceria no dia em que o
contrato mudasse — com a divergência morando num arquivo que nem está no caminho do
build.

A saída é gerada por [`ferramentas/geradordenopython`](../ferramentas/geradordenopython/),
e **`internal/contrato/fidelidade`** compara byte a byte o que o Python e o Go produzem
para a mesma mensagem. Sem esse teste, o gerador seria uma esperança: protobuf não
carrega nomes de campo, e um número de tag trocado vira outro campo em silêncio.

---

## Limites reais do DHT11

Eles aparecem no código, e não como comentário decorativo:

| | |
|---|---|
| Taxa máxima | 1 Hz — e o datasheet recomenda **≥ 5 s** para leitura precisa |
| Resolução | **1 °C e 1 % UR — inteiros.** Não existe 24,3 °C |
| Faixa | 0 a 50 °C, 20 a 90 % UR |
| Exatidão | ±2 °C, ±5 % UR |

Por isso `INTERVALO_DE_AMOSTRAGEM_MS = 5000`: abaixo disso o próprio sensor se aquece e
a medida deriva. Amostrar mais rápido produziria mais números e menos informação.

A faixa vai na **configuração da instalação**, no gateway, como `faixa_minima` e
`faixa_maxima` — ela descreve o **instrumento**, e leitura fora dela é *marcada*, nunca
recusada. Descartar apagaria justamente o sintoma de um transdutor descalibrado ou de um
cabo rompido.

---

## Ligação

```
DHT11            ESP32
  VCC   ──────── 3V3
  DATA  ──────── GPIO 4      (resistor de pull-up 10k entre DATA e VCC)
  GND   ──────── GND
```

O pino é configurável em `PINO_DO_DHT11`. Módulos DHT11 em placa geralmente já trazem
o pull-up.

---

## Como rodar

### 1. Configurar

Edite o topo de `main.py`:

```python
REDE_SSID = "sua-rede"
REDE_SENHA = "sua-senha"
GATEWAY_HOST = "192.168.0.100"      # IP da máquina rodando o gateway
ID_DO_DISPOSITIVO = "esp32-sala-01"  # único na instalação INTEIRA
```

O `ID_DO_DISPOSITIVO` precisa ser único na instalação toda, nunca só dentro de um
gateway: se dois gateways numerassem dispositivos a partir de 1, juntar os dados depois
seria irreversível.

### 2. Copiar para o ESP32

```bash
mpremote connect /dev/ttyUSB0 fs cp no-micropython/synkacore_contrato.py :
mpremote connect /dev/ttyUSB0 fs cp no-micropython/main.py :
mpremote connect /dev/ttyUSB0 reset
```

### 3. Subir o gateway escutando na rede

Por padrão o ingresso escuta em `127.0.0.1`, que o ESP32 não alcança:

```bash
./bin/synkacore-gateway -ingresso 0.0.0.0:8443 -apresentacao 0.0.0.0:8080
```

### 4. Comissionar

O gateway gera o esboço da configuração a partir do que o ESP32 declarou:

```bash
curl http://127.0.0.1:8080/comissionamento/esboco > configuracao/instalacao.yaml
```

Substitua os marcadores `AJUSTAR-...` pelos nomes reais dos pontos e acrescente a faixa
do instrumento:

```yaml
  - dispositivo: esp32-sala-01
    modulo: 0
    canal: 0
    ponto: ambiente.sala.temperatura
    grandeza: temperatura
    unidade: Cel
    faixa_minima: 0.0      # faixa REAL do DHT11
    faixa_maxima: 50.0

  - dispositivo: esp32-sala-01
    modulo: 0
    canal: 1
    ponto: ambiente.sala.umidade
    grandeza: umidade_do_ar
    unidade: "%"
    faixa_minima: 20.0
    faixa_maxima: 90.0
```

Reinicie o gateway com `-instalacao configuracao/instalacao.yaml` e confira:

```bash
curl http://127.0.0.1:8080/comissionamento   # deve vir sem divergência
curl http://127.0.0.1:8080/leituras?limite=10
```

---

## Segurança: dívida declarada

**Este nó fala HTTP em texto claro.** Não é esquecimento — é a ordem de trabalho
escolhida: ver dado de sensor real chegando antes de complicar com certificado, para
não depurar duas coisas novas ao mesmo tempo.

Enquanto for texto claro, **só rode em rede controlada**.

A V2.1 troca por mTLS com CA interna, e é troca de **transporte**, não de desenho: o
MicroPython suporta `ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)` com `load_cert_chain`, e
aceita certificado como `bytes` além de caminho de arquivo.

---

## Duas regras que vieram do nó em Go

Estão no código com o motivo, e valem repetir porque são o que separa este nó de um
script que lê sensor.

**1. A amostragem tem período fixo e nunca espera a rede.** O despacho pode recuar por
segundos quando o gateway cai. Se a amostragem esperasse por ele, a série ganharia
buracos permanentes — e dado que **nunca foi medido** não está em buffer nenhum:
nenhuma retransmissão o traz de volta.

Isso não é teoria. O nó em Go tinha exatamente esse defeito, e o teste de ponta a ponta
flagrou **15 segundos sem nenhuma medição** durante uma queda de 12 segundos.

**2. O nó nunca afirma saber a hora.** O ESP32 não tem relógio com bateria — ao ligar,
começa em 1970. Ele reporta apenas `ticks_ms()`, que é monotônico, e sorteia uma sessão
de boot a cada partida. O gateway ancora esse tempo ao relógio dele, e mantém os três
tempos separados: o bruto, o observado e o estimado.

---

## Limitações conhecidas

| | |
|---|---|
| **Buffer em RAM** | Não sobrevive a reinício do nó. O contrato pede durabilidade em disco para evento discreto; este nó só emite amostras, que toleram perda. Buffer em flash entra se ele for para produção. |
| **RSSI não reportado** | `int32` negativo exige varint de dez bytes em complemento de dois, e o gerador ainda não emite. Custo: perde-se distinguir perda por rede de perda por defeito do nó. |
| **Sem contrapressão diferenciada** | 4xx e 5xx recebem o mesmo tratamento; a distinção entre *descartar* e *retransmitir* entra na V2.1. |
| **Coletado por GC** | Contraria o invariante de "nenhum `malloc` após a inicialização" que a arquitetura assume para nó embarcado. Gerenciável com watchdog; é a razão de o nó de produção ser C++ restrito. |
