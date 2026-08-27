# SynkaCore V1.x — implementação original em Java (preservada)

> Esta pasta é **registro histórico**, não código vivo. Nada aqui é compilado, testado ou
> mantido pela V2.0 em Go. Ela existe para que a implementação anterior permaneça
> **legível e auditável na árvore do projeto**, sem depender de ferramenta de git.

---

## O que é

A implementação completa das versões **V1.0, V1.1 e V1.2** do SynkaCore, escrita em
**Java 25 com Spring Boot 3.5** sobre um build Maven multi-módulo. É o middleware
industrial OT/IT que coletava de CLPs e sensores, normalizava, persistia em TimescaleDB e
expunha via REST — com resiliência Resilience4j e buffer local SQLite garantindo zero
perda de dados durante quedas prolongadas do banco.

| | |
|---|---|
| Linguagem / framework | Java 25 LTS · Spring Boot 3.5 |
| Build | Maven multi-módulo (4 módulos) |
| Arquivos-fonte Java | 38 |
| Linhas de Java | ~2.085 |
| Commit de referência | `a2983f8` — **2026-06-28** |
| Tag | `v1.2-java` |

### Os quatro módulos

| Módulo | Papel |
|---|---|
| `synkacore-domain` | Núcleo puro: `SensorReading`, `ProtocolReader`, `ReadingRepository`, `WorkerState`, settings validadas |
| `synkacore-infrastructure` | TimescaleDB, buffer SQLite, pipeline Resilience4j, OPC UA (Eclipse Milo), simulador de câmara de vácuo |
| `synkacore-collector` | Worker de coleta contínua com três estados operacionais |
| `synkacore-api` | REST — `GET /readings`, `GET /health` com `SELECT 1` real |

A pasta `config/` traz os portões de qualidade do build da época (SpotBugs + FindSecBugs,
PMD 7, Checkstyle), e `.mvn/` ancora `${maven.multiModuleProjectDirectory}` para que os
caminhos dessas configurações continuem resolvendo a partir **desta pasta**.

---

## Por que foi preservada

Duas razões, e a segunda é a que determina que ela fique **visível** em vez de apenas
recuperável por tag:

1. **Continuidade técnica.** A V2.0 em Go não é uma correção da V1.x — é outra
   arquitetura. Poder abrir o código anterior lado a lado com o novo torna cada decisão de
   migração verificável em vez de declarada. Os documentos [`V1.0`](../../docs/V1.0.md),
   [`V1.1`](../../docs/V1.1.md) e [`V1.2`](../../docs/V1.2.md) descrevem o que cada versão
   entregou; esta pasta é o que sustenta aquelas descrições.

2. **Anterioridade e propriedade intelectual.** Este código antecede a bolsa de P&DI
   obtida no SENAI com base no projeto. Mantê-lo na árvore deixa demonstrável, para quem
   for avaliar sem abrir um terminal, que a capacidade técnica e o produto **já existiam
   antes** do fomento — e não foram desenvolvidos durante ou por causa dele.

### Sobre o que de fato prova a data

Vale ser preciso, porque isso importa numa discussão de propriedade intelectual:

- O que ancora a anterioridade é a **data dos commits** (`a2983f8`, 2026-06-28) e a tag
  `v1.2-java`, não a presença dos arquivos nesta pasta. A pasta serve à legibilidade.
- Histórico de git pode ser reescrito, então ele é evidência **corroborante**, não prova
  criptográfica por si só. O que endurece a datação é ancoragem externa: o *push* para um
  remoto com registro do provedor, e sobretudo a **própria submissão ao SENAI**, que
  carrega data institucional independente.
- Se houver necessidade formal de comprovação, o caminho mais forte é registrar o depósito
  do código-fonte (INPI, programa de computador) referenciando este commit.

---

## Como reproduzir o build da época

```bash
cd legado/java-v1.2
mvn clean verify
```

Exige **JDK 25** e **Maven 3.9+**. Os testes de integração (`*IT`) sobem TimescaleDB via
Testcontainers e são pulados automaticamente onde não há Docker.

> **Não verificado nesta máquina.** Maven não está instalado no ambiente onde a migração
> para Go foi feita, então o build acima não foi reexecutado durante a transição. Ele
> passava no commit `a2983f8`, conforme registrado em [`QUALIDADE.md`](../../docs/QUALIDADE.md)
> — mas isso é o registro da época, não uma reverificação.

---

## O que a V2.0 herdou daqui

A reescrita em Go não descartou o raciocínio desta implementação. O que sobreviveu, e onde
foi parar:

| Conceito da V1.x | Destino na V2.0 |
|---|---|
| Zero perda em queda do banco (buffer SQLite) | Virou **estrutural**: toda escrita vai primeiro ao diário durável, e um projetor assíncrono alimenta o banco de consulta. Deixa de ser caminho de exceção. |
| Três estados do Worker (CONECTADO / RECONECTANDO / DEGRADADO) | Preservado como estado operacional observável do gateway |
| Pipeline de resiliência (circuit breaker → retry → timeout) | Reescrita em Go, e reposicionada: protege o **projetor**, não o caminho de aquisição |
| Simulador de câmara de vácuo | Vira o **nó em software**, que remove a dependência de hardware físico |
| Health check real (`SELECT 1`, nunca mentir sobre saúde) | Mantido como princípio |
| Configuração validada no startup, falha ruidosa | Mantido como princípio |

O que **não** sobreviveu, e por quê, está registrado nos ADRs da V2.0 em [`../../docs/`](../../docs/).
