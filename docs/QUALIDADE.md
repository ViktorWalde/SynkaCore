# SynkaCore — Garantias de Qualidade

O build do SynkaCore é uma sequência de portões (quality gates). Qualquer violação
derruba o build: disciplina é imposta pela ferramenta, não pela boa vontade. Tudo roda
com `mvn verify`.

---

## Compilador no modo rigoroso

`maven-compiler-plugin` com:
- `-Xlint:all` — todos os lints do `javac` ligados (unchecked, deprecation, serial, cast, etc.).
- `failOnWarning` (equivalente a `-Werror`) — **qualquer warning derruba o build**.
- `-parameters` — nomes de parâmetros preservados no bytecode.

Consequência prática: não existe warning "ignorado". Cada um vira erro e é tratado (ex.: as exceções de domínio ganharam `serialVersionUID`).

---

## Disciplina de dependências

`maven-enforcer-plugin` com as regras:
- `requireMavenVersion` `[3.9,)` e `requireJavaVersion` `[25,)`.
- `dependencyConvergence` — todas as versões transitivas de uma mesma dependência precisam convergir.
- `requireUpperBoundDeps` — a versão resolvida nunca é menor que a requisitada por uma transitiva.
- `banDuplicatePomDependencyVersions` — sem dependências duplicadas no POM.

As versões são governadas pelo BOM do Spring Boot e por um `dependencyManagement` central no POM pai.

### Auditoria sob demanda
- **Atualizações**: `mvn versions:display-dependency-updates` e `display-plugin-updates`.
- **Vulnerabilidades (CVE)**: profile opt-in `security` com OWASP dependency-check —
  `mvn -Psecurity verify` (requer `NVD_API_KEY`). Falha o build em qualquer dependência com CVSS ≥ 7.

---

## Análise estática de bugs e segurança

`spotbugs-maven-plugin` com **esforço máximo** e **threshold Low** (mais sensível), somado ao
plugin **FindSecBugs** (regras de segurança). Exclusões ficam em `config/spotbugs/exclude.xml`,
cada uma com justificativa explícita (são falso-positivos no contexto OT, não bugs reais).

Achados reais corrigidos nesta blindagem, por exemplo: construtor que lançava sem a classe ser
`final` (vetor de finalizer attack) → classe tornada `final`.

---

## Análise estática de código-fonte

`maven-pmd-plugin` (PMD 7) com as categorias *bestpractices*, *errorprone* e *performance*
(`config/pmd/ruleset.xml`). Achados reais corrigidos, por exemplo: re-lançamento que perdia o
stack trace da causa raiz → `addSuppressed` preservando ambas as falhas; inicializador de campo
redundante removido.

---

## Disciplina de estilo

`maven-checkstyle-plugin` (`config/checkstyle/checkstyle.xml`): imports (sem `*`, sem não usados),
nomenclatura, chaves obrigatórias, espaçamento, e armadilhas comuns (`==` em String, fall-through,
`@Override` ausente). Mantido pragmático: documentação/Javadoc não é obrigatória — bugs são cobertos
por SpotBugs/PMD.

---

## Testes

Separados por velocidade e isolamento, via Surefire (unitários) e Failsafe (integração):

| Camada | Marca | O que cobre |
|---|---|---|
| Unitários | `*Test` | Decorator, parsing do buffer, pipeline de resiliência, simulador, state tracker, controllers (MockMvc) |
| Integração | `*IT` | TimescaleDB real (Testcontainers) e SQLite real — DDL, SQL e mapeamento de tipos |

Os `*IT` são pulados automaticamente onde não há Docker. Os tempos da pipeline de resiliência são
injetáveis para que os testes sejam rápidos sem mudar o comportamento de produção.

---

## Configuração compartilhada

Os arquivos de configuração das ferramentas ficam em `config/` na raiz e são referenciados por
todos os módulos via `${maven.multiModuleProjectDirectory}` (a pasta `.mvn/` ancora a raiz).
