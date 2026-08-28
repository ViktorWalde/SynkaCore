# SynkaCore

> Plataforma de aquisição industrial **OT/IT** para borda: coleta, valida, torna durável e
> expõe dados de chão de fábrica — **sem perder nada**, sem nuvem e sem depender de que o
> banco de consulta esteja de pé.

![Go](https://img.shields.io/badge/Go-1.26-00ADD8)
![Contrato](https://img.shields.io/badge/contrato-Protobuf-blue)
![Diário](https://img.shields.io/badge/diário-SQLite%20(Go%20puro)-003B57)
![Consulta](https://img.shields.io/badge/consulta-TimescaleDB%20PG17-blueviolet)
![Binário](https://img.shields.io/badge/binário-estático%2C%20sem%20cgo-brightgreen)

---

## O que é

O SynkaCore é a ponte confiável entre o **chão de fábrica (OT)** e os **sistemas de gestão
(IT)**. Origens de dado — equipamento embarcado, ou o nó em software que acompanha o projeto
— entregam remessas ao gateway por um contrato de fio versionado. O gateway valida, torna
**durável no disco local** e confirma. Um projetor assíncrono alimenta o banco de séries
temporais que os dashboards consultam.

```mermaid
flowchart LR
    subgraph OT["🏭 Chão de fábrica"]
        NO["synkacore-no<br/>origem do dado"]
    end
    subgraph GW["⚙️ synkacore-gateway"]
        ING["ingestão"]
        DIA[("diário SQLite<br/>registro autoritativo")]
        PRO["projetor"]
        APR["apresentação"]
    end
    subgraph IT["📊 Escritório"]
        GRA["Grafana"]
        OPE["operador"]
    end
    TS[("TimescaleDB<br/>modelo de leitura")]

    NO -->|"remessa protobuf"| ING
    ING -->|"grava ANTES de confirmar"| DIA
    ING -.->|"confirma até a sequência N"| NO
    DIA --> PRO --> TS
    TS --> GRA
    APR --> OPE
    DIA --> APR
```

---

## A propriedade central

**A remessa só é confirmada depois de estar durável no disco.** Se a gravação falha, o nó
não recebe confirmação e retransmite. Se o gateway cai no meio, o nó retransmite. Se o
TimescaleDB cai, **a aquisição não percebe** — o dado continua entrando no diário e a
projeção retoma sozinha quando o banco volta.

Isso não é um mecanismo de emergência; é o único caminho que existe.

> Na V1.x, zero perda dependia de um caminho de exceção — um buffer que só era exercitado
> durante a falha. A auditoria da V1.2 encontrou esse caminho **desligado por um erro de
> tipo no construtor**: o buffer estava registrado e nunca era usado, e o dado se perdia
> como antes de ele existir. Um caminho que quase nunca roda é um caminho que não funciona.
> Ver [V2.0](docs/V2.0.md).

### Verificado, não afirmado

Teste de ponta a ponta com o gateway derrubado por 14 segundos enquanto o nó continuava
amostrando a 2 Hz:

```
registros gravados  : 123
sequências faltando : NENHUMA
duplicatas          : NENHUMA
lacunas de amostragem: NENHUMA
```

### E medido, não estimado

Capacidade sob carga, contra disco real — a pergunta que estava aberta desde a V1.x:

| Origens | Envelopes/s | p50 | p99 |
|---|---|---|---|
| 10 | 950 | 25 ms | 51 ms |
| 200 | **18.999** | 494 ms | 945 ms |

Teto de **~20.000 envelopes/s** neste hardware. Detalhes e o gargalo medido em
[V2.3](docs/V2.3.md).

### E declarado, não inferido

Acima do teto o gateway **diz** que está cheio, em vez de deixar a origem descobrir
pelo tempo de resposta — e não recusa as duas classes de dado do mesmo jeito. As
duas cargas abaixo rodaram ao mesmo tempo, contra o mesmo gateway, na mesma fila:

| Carga simultânea | Envelopes/s | Recusadas com `429` |
|---|---|---|
| 400 origens de **amostra** | 18.869 | **1.537** |
| 20 origens de **evento discreto** | 1.000 | **nenhuma** |

Amostra prefere ser recusada a esperar, porque a próxima repõe quase a mesma
informação. Evento discreto prefere esperar, porque não existe próximo que o
reponha. A cauda de latência caiu junto: p99 de **5,6 s para 1,90 s**. Ver
[V2.4](docs/V2.4.md).

---

## Duas origens, o mesmo contrato

O gateway não sabe o que existe do outro lado do fio — só o contrato. Hoje há duas
implementações de nó, e o gateway não distingue as duas:

| Nó | O que é |
|---|---|
| [`internal/no`](internal/no/) (Go) | Simula uma **câmara de vácuo de curtimento** — temperatura, pressão, estado de máquina e contagem, em ciclo de 3 minutos. Roda sem hardware nenhum. |
| [`no-micropython/`](no-micropython/) | **ESP32 com DHT11 real**, medindo temperatura e umidade do ar. |

Serialização, lote, contrapressão, retransmissão, idempotência e ancoragem de tempo são
exercitados de verdade nos dois. Trocar simulação por hardware deixa de ser uma
integração: passa a ser uma troca de quem gera os números.

O codificador protobuf do nó MicroPython é **gerado do `.proto`**, e
[`internal/contrato/fidelidade`](internal/contrato/fidelidade/) compara **byte a byte** o
que o Python e o Go produzem para a mesma mensagem. Sem esse teste o gerador seria uma
esperança: protobuf não carrega nomes de campo, e um número de tag trocado vira outro
campo em silêncio.

O firmware de produção será **C++ restrito sobre ESP-IDF** — decisão registrada em
[`docs/NO-EMBARCADO.md`](docs/NO-EMBARCADO.md), com o gatilho e as travas. Não está ativa.

---

## Como rodar

### Sem infraestrutura nenhuma

A aquisição funciona completa e o dado fica durável. Só falta o gráfico.

```bash
make compilar

./bin/synkacore-gateway     # terminal 1
./bin/synkacore-no          # terminal 2

curl http://127.0.0.1:8080/saude
curl 'http://127.0.0.1:8080/leituras?limite=10'
```

### Com significado: qual equipamento, que grandeza, em que unidade

Sem configuração da instalação, o gateway grava `canal 0 = 24,7` — verdade que não
responde nada. Com ela, cada leitura carrega o ponto de medição, a grandeza e a
unidade, e o `/comissionamento` denuncia canal trocado no painel.

Não escreva o arquivo do zero — o gateway o gera a partir do que as origens já
declararam, e só sobra nomear os pontos:

```bash
./bin/synkacore-gateway &
./bin/synkacore-no &

curl http://127.0.0.1:8080/comissionamento/esboco > configuracao/instalacao.yaml
# edite: substitua cada AJUSTAR-... pelo nome real do ponto de medição

./bin/synkacore-gateway -instalacao configuracao/instalacao.yaml
curl http://127.0.0.1:8080/comissionamento
```

### Com o modelo de leitura e os dashboards

```bash
make infra                  # TimescaleDB + Grafana
make gateway-completo       # terminal 1
make no                     # terminal 2
```

Grafana em `http://localhost:3000` (`admin`/`admin`), com a fonte de dados já provisionada.

### Pré-requisitos

- [Go 1.26+](https://go.dev/dl/) — **só isso** para compilar e rodar
- Docker ou Podman, apenas para o estágio de consulta
- `protoc` + `protoc-gen-go`, apenas para regerar o contrato

---

## Endpoints

Dois servidores, em interfaces separadas, porque o gateway fica entre duas redes.

**Ingresso — lado de chão de fábrica** (`127.0.0.1:8443`), com mTLS

| Endpoint | Descrição |
|---|---|
| `POST /ingestao` | Recebe uma remessa protobuf; devolve a confirmação com a faixa durável |

Com credencial configurada, o certificado de cliente é **exigido** e a identidade que
a remessa reivindica é confrontada com a que o certificado prova. Divergência é
recusada com `403`. O gateway também serve tempo por UDP, para que origens sem
relógio de bateria consigam validar o certificado dele.

**Apresentação — lado de escritório** (`127.0.0.1:8080`), somente leitura

| Endpoint | Descrição |
|---|---|
| `GET /saude` | Estado do diário e da projeção, verificados de verdade |
| `GET /leituras?limite=N` | Registros recentes do diário, já decodificados |
| `GET /contrato` | Tipos de conteúdo que este gateway reconhece |
| `GET /comissionamento` | Desacordos entre o que as origens declaram e o que a instalação configura |
| `GET /comissionamento/esboco` | YAML de configuração gerado a partir do que as origens já declararam |

O `/saude` reporta os dois estágios **separados**, e a distinção decide se alguém é
acordado:

```json
{"journal":"available","projection":"degraded","ingestion":"shedding",
 "ingestion_queue":399,"ingestion_cost_us":4076,
 "ingestion_shed_samples":404,"ingestion_shed_events":0, ...}
```

`journal` falhando significa que o sistema está perdendo a capacidade de aceitar dado.
`projection` falhando significa que o dado está salvo e os dashboards estão atrasados.
`ingestion` em `shedding` significa que o gateway está cheio e as origens estão
bufferizando — não é falha, e por isso o status HTTP continua 200.
`ingestion_cost_us` é o custo medido de uma remessa neste disco, e responde a
pergunta que decide a ação: a mídia é lenta, ou o orçamento é apertado?

---

## Arquitetura

Hexagonal enxuto, aplicado onde ele paga por si. A regra de dependência é única:

```mermaid
flowchart TD
    ADA["adaptador<br/>HTTP, SQLite, TimescaleDB, codec"] --> APL["aplicação<br/>ingestão, projeção"]
    APL --> DOM["domínio<br/>envelope, classes, identidades, tempo"]
    ADA --> PLA["plataforma<br/>falha, relógio, resiliência"]
    APL --> PLA
```

O domínio não importa HTTP, banco, arquivo nem relógio. Não é purismo: é o que permite
testar a regra de tempo e a de idempotência sem subir Postgres.

**Onde deliberadamente não abstraímos**, porque abstração sem segunda implementação é só
indireção:

| Não abstraído | Motivo |
|---|---|
| Diário SQLite | É a definição de durabilidade do sistema, não uma escolha. Nunca haverá um segundo. O teste usa arquivo temporário, que é mais fiel que um dublê. |
| Logging | `log/slog` direto. Envolver o logger só produziria uma API pior. |
| Relógio | Injetado como interface de **dois** métodos, e os dois existem por uma razão que custou um achado bloqueante — ver abaixo. |

### Nomes

Identificadores em **português sem acento**, porque o vocabulário do domínio já é português
em toda a documentação. Inglês fica onde a linguagem impõe (`main`, `Error`, `String`), onde
o compilador reconhece a estrutura (`internal/` **não é escolha**: o Go impõe que pacotes
ali não sejam importáveis de fora) e nos identificadores que **saem do processo** — rótulo
de métrica e coluna de banco são consumidos por Prometheus, Grafana e SQL.

Não existe `utils`, `helpers`, `common` ou `models`: são gavetas onde código duplicado se
esconde, porque nenhum desses nomes diz o que **não** pertence ali.

---

## Invariantes, e como cada um é travado

Documentar "não duplique" não sustenta nada. Cada item abaixo é uma trava real no código.

| Invariante | Trava |
|---|---|
| **Identidade provada, não afirmada** | O `id_do_dispositivo` da remessa é confrontado com o nome comum do certificado que o TLS validou. Sem isso, um dispositivo com credencial legítima pode gravar dado sob a identidade do vizinho — e o resultado é plausível, indetectável depois. |
| **Orçamento de evento nunca abaixo do de amostra** | `NovaAdmissao` recusa a partida. Invertido, o gateway passaria a recusar parada de máquina antes de leitura de temperatura — continuando a aceitar dado e a responder saudável enquanto a contagem de paradas ficasse permanentemente errada. |
| **Saturação recusa amostra antes de evento** | A admissão dá orçamentos de espera diferentes por `ClasseDeDado`. Sem isso, contrapressão seria um limitador de taxa — e um limitador de taxa recusa uma parada de máquina com a mesma naturalidade com que recusa a milésima leitura de temperatura. |
| **Um ponto de validação por conceito** | `NovoEnvelope` é o único construtor de mensagem. Campos não exportados ⇒ possuir um `Envelope` é prova de que ele é válido. Não existe "validar de novo por segurança". |
| **Interface nomeada em vez de asserção anônima** | `TestConteudoEnderecadoCasaComOContrato` lê o descritor e exige que conteúdo com campo `endereco` implemente `ConteudoEnderecado`, e vice-versa. Nasceu de um defeito real: uma asserção para interface anônima que nunca casava, deixando o enriquecimento inteiro como código morto. |
| **Um catálogo que recusa duplicata na inicialização** | `NovoCatalogoDeConteudo` rejeita tipo repetido. Dois arquivos definindo o mesmo tipo derrubam o gateway **no boot**, não em produção. |
| **O catálogo cobre o contrato** | `TestTodoConteudoDoContratoTemDefinicao` lê o descritor do protobuf por reflexão. Acrescentar uma mensagem ao contrato sem ensinar o gateway a interpretá-la reprova o build. |
| **Exaustividade sobre enum** | Os `switch` sobre `ClasseDeDado`, `EstadoDeMaquina` e `falha.Categoria` **não têm `default`**, e o linter roda com `default-signifies-exhaustive: false`. |
| **Um projetor genérico, não um por tipo** | Cada conteúdo declara o que contribui ao modelo de leitura; o projetor não conhece tipo nenhum. |
| **Uma taxonomia de erro, um mapeador por adaptador** | `falha.Categoria` é o vocabulário único; `statusDe` é o único condicional sobre erro no adaptador HTTP. |
| **Nenhum código duplicado** | `dupl`, `goconst`, `gocognit` e `nestif` no `golangci-lint`, verificados a cada `make verificar`. |

---

## Duas coisas que valem ler

### O relógio tem dois métodos, e isso resolve um achado bloqueante

Em Go, `time.Time` carrega uma leitura monotônica — e **qualquer `.UTC()` a descarta**. A
partir dali, subtrair dois instantes usa o relógio de parede, que anda para trás quando o
NTP corrige. Numa trilha que precisa provar *quando* algo aconteceu, isso deixa de ser
corretude e vira conformidade.

`plataforma/relogio` mantém as duas leituras separadas e **comparáveis entre si**: um acerto
de hora move só a parede, então a divergência vira mensurável. O `relogio.Falso` tem
`Avancar` (move as duas juntas, que é o tempo normal) e `DarDegrau` (move só a parede) —
um degrau real é impossível de reproduzir em CI; aqui é uma chamada de método.

### O nó não pode parar de medir

O teste de ponta a ponta encontrou o nó **parando de amostrar por 15 segundos** durante uma
queda de 12 segundos do gateway: amostragem e despacho dividiam um `select`, e o recuo do
despacho dormia bloqueando o temporizador.

A distinção que isso revela é a que importa. O buffer protege contra perder dado **no
caminho** — e para isso funcionava. Mas dado que **nunca foi medido** não está em buffer
nenhum, e nenhuma retransmissão o traz de volta.

Hoje são dois laços independentes, e um teste de regressão mede o **espaçamento entre
tempos ligados** para garantir que continue assim.

---

## Estrutura

```
SynkaCore/
├── contrato/proto/       # o .proto — fonte única de verdade do fio
├── cmd/                  # raízes de composição: o único lugar com wiring
├── internal/
│   ├── contrato/v1/      # gerado do .proto, versionado de propósito
│   ├── dominio/          # regras. Sem I/O, sem framework, sem relógio.
│   ├── aplicacao/        # ingestão e projeção
│   ├── adaptador/        # entrada, saída, codec
│   ├── no/               # a origem do dado e a simulação de processo
│   └── plataforma/       # falha, relógio, resiliência, identificador
├── migracoes/            # esquema do modelo de leitura
├── implantacao/grafana/  # provisionamento como código
├── legado/java-v1.2/     # a implementação V1.x, preservada
└── docs/
```

---

## Build e qualidade

```bash
make verificar    # formatação, vet, testes com -race, linter, contrato em dia
make compilar     # binários estáticos, sem cgo
make contrato     # regera o Go a partir do .proto
make no-micropython  # regera o codificador do nó a partir do .proto
make cobertura    # relatório de cobertura em HTML
make medir        # benchmarks do diário contra disco real
make carga        # gerador de carga contra um gateway no ar
make carga CLASSE=evento  # a mesma carga como evento discreto, que tem outro orçamento
```

`make verificar` é o portão completo — qualquer falha derruba o build. Disciplina imposta
pela ferramenta, não pela boa vontade.

O binário é **verdadeiramente estático**, sem dependência de runtime. É o argumento que
decidiu a linguagem: implantar é copiar **um arquivo** e uma unidade systemd, e reverter é
manter o arquivo anterior. Numa planta sem internet, atualizada pelo notebook de um técnico,
isso vale mais que qualquer vantagem de velocidade bruta.

---

## Documentação

- **[V2.0](docs/V2.0.md)** — a reescrita: por que foi antecipada, o que mudou, o que foi
  encontrado no caminho.
- **[V2.2](docs/V2.2.md)** — configuração da instalação: como o dado ganha significado, e a
  rede de proteção que denuncia canal trocado no painel.
- **[V2.1](docs/V2.1.md)** — mTLS com CA interna, identidade autenticada contra
  reivindicada, e o servidor de tempo que torna a validação possível numa origem sem
  relógio.
- **[V2.3](docs/V2.3.md)** — capacidade medida, onde está o gargalo, e os painéis do
  Grafana como código.
- **[V2.4](docs/V2.4.md)** — contrapressão explícita: o gateway passa a dizer que
  está cheio, com uma espera que ele mediu, e recusa amostra antes de recusar
  evento discreto.
- **[V2.5](docs/V2.5.md)** — o orçamento de espera vira promessa declarada na
  instalação, e o gateway calibra o próprio disco na partida.
- **[Visão geral visual](docs/VISAO-GERAL.md)** — diagramas de fluxo e cenários de queda.
- **[Trade-offs](docs/TRADE-OFFS.md)** — decisões técnicas e seus custos.
- **[Qualidade](docs/QUALIDADE.md)** — os portões do build.
- **[Propriedade intelectual](docs/PROPRIEDADE-INTELECTUAL.md)** — anterioridade, manifesto
  criptográfico e o caminho do registro no INPI.
- **[Nó embarcado](docs/NO-EMBARCADO.md)** — por que C++ restrito e não C, o subconjunto,
  e as travas que o tornam aceitável.
- **Histórico V1.x** — [V1.0](docs/V1.0.md) · [V1.1](docs/V1.1.md) · [V1.2](docs/V1.2.md),
  com o código em [`legado/java-v1.2/`](legado/java-v1.2/).

---

## Roadmap

| Versão | Status | Entrega |
|---|---|---|
| V1.0 | ✅ Concluída | Fundação em Java: coleta, persistência, API REST |
| V1.1 | ✅ Concluída | Resiliência, observabilidade, health check real |
| V1.2 | ✅ Concluída | Buffer local SQLite; auditoria estrutural |
| V1.3–V1.5 | ❌ Canceladas | Exigiam hardware físico — ver [V2.0](docs/V2.0.md) |
| **V2.0** | 🚧 Em desenvolvimento | Reescrita em Go, contrato de fio, durabilidade estrutural, nó em software |
| **V2.2** | 🚧 Em desenvolvimento | Configuração da instalação: canal → ponto de medição, catálogo de motivos, comissionamento |
| **V2.1** | 🚧 Em desenvolvimento | mTLS com CA interna, identidade autenticada vs. reivindicada, servidor de tempo |
| **V2.3** | 🚧 Em desenvolvimento | Capacidade medida, gargalo identificado, painéis do Grafana como código |
| **V2.4** | 🚧 Em desenvolvimento | Contrapressão explícita: saturação declarada com `429`, `Retry-After` medido, e reserva por classe de dado |
| **V2.5** | 🚧 Em desenvolvimento | Orçamento de espera na configuração da instalação, e calibração do disco na partida |
