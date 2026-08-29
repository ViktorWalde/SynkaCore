# Implantar o SynkaCore numa planta

O gateway é **um binário estático, sem cgo e sem dependência de runtime**. Isso não é
detalhe de build: é o argumento que decidiu a linguagem do projeto. Implantar é copiar
um arquivo e uma unidade systemd; reverter é manter o arquivo anterior no disco.

Numa planta sem internet, atualizada pelo notebook de um técnico, isso vale mais que
qualquer vantagem de velocidade bruta.

---

## O que o pacote contém

```
synkacore-gateway              o gateway (binário estático)
synkacore-credencial           emissão da CA e dos certificados da instalação
synkacore-gateway.service      unidade systemd, já endurecida
synkacore-gateway.env          configuração de PROCESSO (endereços e caminhos)
instalacao.exemplo.yaml        configuração da INSTALAÇÃO (o significado do dado)
migracoes/                     esquema do modelo de leitura
SHA256SUMS                     resumo de cada arquivo
```

Gerado com `make pacote`.

**Dois arquivos de configuração, e a separação é deliberada.** O `.env` carrega o que
muda de máquina para máquina — endereços, caminhos. O `instalacao.yaml` carrega o que
o dado **significa**: pontos de medição, unidades, orçamentos de admissão. O primeiro
é operação; o segundo é versionado, revisável e é o que explica a série histórica anos
depois.

---

## Instalação

```bash
tar -xzf synkacore-gateway-*.tar.gz && cd synkacore-gateway-*
sha256sum -c SHA256SUMS

sudo useradd --system --no-create-home --shell /usr/sbin/nologin synkacore
sudo install -d -o synkacore -g synkacore -m 0750 /var/lib/synkacore
sudo install -d -m 0755 /etc/synkacore

sudo install -m 0755 synkacore-gateway /usr/local/bin/
sudo install -m 0644 synkacore-gateway.service /etc/systemd/system/
sudo install -m 0644 synkacore-gateway.env /etc/synkacore/gateway.env

sudo systemctl daemon-reload
sudo systemctl enable --now synkacore-gateway
```

Confira:

```bash
curl http://127.0.0.1:8080/saude
```

```json
{"journal":"available","projection":"disabled","ingestion":"accepting","ingestion_cost_us":761, ...}
```

`ingestion_cost_us` já preenchido significa que o gateway mediu o disco desta máquina
na partida. Se ele estiver alto — dezenas de milhares —, a mídia é lenta e a planta
vai saturar mais cedo do que o dimensionamento previa. Melhor descobrir agora.

---

## Comissionamento, na ordem

O gateway sobe **sem** configuração de instalação e **sem** banco de consulta. Isso é
proposital: um sistema que só liga depois de configurado por completo não pode ser
comissionado por etapas, e ficaria mudo justamente na fase em que se quer verificar se
ele funciona.

**1. Ligue as origens e confirme que o dado entra.**

```bash
curl 'http://127.0.0.1:8080/leituras?limite=10'
```

Os canais aparecem sem significado — `canal 0 = 24,7`. É o estado esperado agora.

**2. Gere o esboço da configuração a partir do que as origens declararam.**

```bash
curl http://127.0.0.1:8080/comissionamento/esboco > /tmp/instalacao.yaml
```

Dispositivo, módulo, canal, grandeza e unidade vêm preenchidos e **casam com o fio por
construção**. Não escreva o arquivo do zero: digitar módulo e canal à mão para dezenas
de entradas é exatamente onde o erro de comissionamento nasce.

**3. Nomeie os pontos de medição.** É a única parte que só uma pessoa sabe — substitua
cada `AJUSTAR-...` pelo nome real. Use hierarquia:
`linha-2.prensa-01.temperatura-mancal` permite consultar uma linha inteira depois;
`temp1` não permite nada.

**4. Aplique e confira.**

```bash
sudo install -m 0644 /tmp/instalacao.yaml /etc/synkacore/instalacao.yaml
sudo systemctl restart synkacore-gateway
curl http://127.0.0.1:8080/comissionamento
```

O relatório denuncia desacordo entre o que a origem declara medir e o que a
configuração diz — **canal trocado no painel deixa de ser erro silencioso.**

---

## Ligando o modelo de leitura

O TimescaleDB é opcional e não está no caminho de aquisição. Se ele cair, o dado
continua entrando no diário e a projeção retoma sozinha.

**As migrações são aplicadas por você, nunca pelo gateway.** Um serviço que altera o
próprio esquema na partida transforma um rollback de binário numa migração reversa não
planejada — e isso numa planta a 400 km, num sábado.

```bash
for m in migracoes/*.sql; do psql "$BANCO" -f "$m"; done
```

Depois aponte `SYNKACORE_BANCO` no `.env` e reinicie. Se o esquema estiver
desatualizado, o gateway **recusa a partida** dizendo qual migração falta — em vez de
subir e falhar em toda gravação horas depois.

---

## Atualizar e reverter

**Atualizar:**

```bash
sudo cp /usr/local/bin/synkacore-gateway /var/lib/synkacore/synkacore-gateway.anterior
sudo install -m 0755 synkacore-gateway /usr/local/bin/
sudo systemctl restart synkacore-gateway
```

**Reverter:**

```bash
sudo install -m 0755 /var/lib/synkacore/synkacore-gateway.anterior /usr/local/bin/synkacore-gateway
sudo systemctl restart synkacore-gateway
```

Nenhuma das duas operações perde dado. Durante o reinício as origens bufferizam e
retransmitem; as âncoras de tempo estão persistidas no diário.

Guarde o binário anterior **antes** de sobrescrever. É a única coisa que a reversão
precisa, e é a única que não dá para recuperar depois.

---

## O que a unidade systemd garante

| Diretiva | Por quê |
|---|---|
| `Restart=always`, `StartLimitIntervalSec=0` | A aquisição é o único caminho que nunca pode parar. O padrão do systemd desiste após algumas tentativas e deixa a unidade em `failed` — "desistiu às 3h" é a pior resposta possível numa planta sem ninguém por perto. |
| `After=network-online.target`, sem `Requires` | O gateway precisa da rede para **receber**, não para funcionar. `Requires` o derrubaria numa oscilação de rede — parando de aceitar dado por causa do problema que ele existe para atravessar. |
| `TimeoutStopSec=30` | O desligamento limpo tem prazo próprio de 15 s para não cortar uma remessa no meio da gravação. Menos que isso mataria o processo no meio do que ele já sabe fazer direito. |
| `ProtectSystem=strict` + `ReadWritePaths=/var/lib/synkacore` | O diário é a única coisa que este processo grava. Declarar isso é a definição mais curta e verificável do que ele faz no disco. |
| `AmbientCapabilities=CAP_NET_BIND_SERVICE` | A porta 123 do servidor de tempo exige privilégio. A capacidade vem sozinha, e não rodando como root. Se o servidor de tempo não for usado, remova as duas linhas e o processo fica sem capacidade nenhuma. |

---

## Diagnóstico

| Sintoma | Onde olhar |
|---|---|
| `journal` diferente de `available` | O sistema perdeu a capacidade de **aceitar** dado. Único caso que acorda alguém. |
| `projection` em `degraded` | O dado está salvo; os dashboards atrasaram. Não acorda ninguém. |
| `ingestion` em `shedding` | O gateway está cheio e as origens estão bufferizando. Compare `ingestion_cost_us` com o orçamento: custo alto é mídia lenta, custo normal é orçamento apertado. |
| `ingestion_shed_events` maior que zero | O teto real foi ultrapassado — o gateway recusou dado que nenhuma amostra seguinte repõe. |
| Origem recusada com `403` | O identificador que ela reivindica não é o que o certificado prova. Corrija a configuração do dispositivo. |

```bash
journalctl -u synkacore-gateway -f
```
