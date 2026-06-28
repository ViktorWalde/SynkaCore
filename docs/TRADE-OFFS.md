# SynkaCore — Trade-offs Arquiteturais

Documento honesto sobre as decisões que abrem mão de algo para priorizar outra coisa. Nenhuma arquitetura é perfeita. Cada escolha tem custo. Este documento existe para que o custo de cada escolha seja explícito, e para que decisões futuras de evolução tenham contexto sobre o porquê das escolhas atuais.

---

## Buffer local em SQLite

### A escolha
Buffer local em SQLite com tabela append-only.

### O que se prioriza
Simplicidade operacional, garantia de durabilidade ACID, ausência de servidor para gerenciar, arquivo único portável, zero custo de licença.

### Do que se abre mão
- **Throughput de escrita massivo**: SQLite faz lock de banco inteiro durante escrita. Para múltiplas threads escrevendo em paralelo a alta frequência, é gargalo. RocksDB ou LMDB teriam melhor performance concorrente.
- **Replicação nativa**: SQLite não replica entre nós. Se o disco da máquina morrer, o buffer morre junto. Aceitável porque o buffer é transitório e os dados sobreviventes estão no TimescaleDB.
- **Queries analíticas no buffer**: SQLite suporta SQL completo mas o buffer não foi projetado para isso. É fila, não banco analítico.

### Quando reconsiderar
Se o volume de leituras passar de 100 por segundo sustentado, ou se houver necessidade de buffer compartilhado entre múltiplas instâncias do Collector.

---

## Sincronização do buffer dentro de save

### A escolha
O `ResilientReadingRepository` sincroniza o buffer no mesmo método que grava no primário, em vez de ter um serviço agendado separado com timer próprio.

### O que se prioriza
Simplicidade. Uma classe, um fluxo, sem coordenação de threads. Sincronização acontece quando há "garantia" de que o primário voltou, porque a gravação atual passou.

### Do que se abre mão
- **Throughput durante recuperação**: cada `save` que sincroniza 10 leituras espera essas 10 gravações terminarem antes de retornar. Se o Worker está produzindo a 1Hz e o sync está drenando a 5Hz, o Worker fica parcialmente bloqueado durante recuperação.
- **Drenagem em paralelo**: um serviço separado poderia drenar o buffer enquanto o Worker continua coletando, dobrando o throughput durante recuperação.

### Quando reconsiderar
Se em produção real medirmos que o tempo de `syncBuffer` está aumentando a latência do ciclo do Worker em mais de 20%.

---

## Estado do Worker com volatile em vez de máquina de estados formal

### A escolha
`WorkerStateTrackerImpl` usa um campo `volatile` para representar o estado atual, com transições simples sem validação.

### O que se prioriza
Performance, simplicidade, leitura cross-thread sem locks.

### Do que se abre mão
- **Transições inválidas**: nada impede transição direta de CONECTADO para DEGRADADO sem passar por RECONECTANDO. Em sistemas com regras estritas de estado, isso seria problema. Aqui é aceitável porque o estado é informativo, não controle.
- **Histórico de transições**: a transição anterior é apagada quando a nova chega. Não há log estruturado de quando o sistema entrou e saiu de cada estado. Os logs textuais cobrem isso mas não permitem queries.
- **Lógica condicional na transição**: máquina de estados formal permitiria "só transita para X se condição Y". Aqui qualquer notificação é aplicada imediatamente.

### Quando reconsiderar
Quando o estado do Worker for usado para tomada de decisão crítica (ex: bloquear escrita), não apenas observabilidade. Aí máquina de estados formal com biblioteca como Spring StateMachine faz sentido.

---

## Decorator Pattern para resiliência de persistência

### A escolha
`ResilientReadingRepository` envolve `SensorReadingRepository` e `SensorReadingLocalBufferRepository`, expondo uma única interface `ReadingRepository`.

### O que se prioriza
Separação de responsabilidades, princípio aberto/fechado, Worker desacoplado de detalhes de fallback.

### Do que se abre mão
- **Configuração do DI fica mais complexa**: três classes registradas em camadas, com beans explícitos e o decorator marcado `@Primary`. Mais código de wire-up.
- **Ordem das camadas é frágil**: se alguém registrar o `SensorReadingRepository` diretamente como `ReadingRepository` em outro contexto que consuma a Infrastructure, vai bypassar todo o sistema de buffer sem erro. Foi exatamente o bug 1 que aconteceu durante a V1.2.
- **Stack trace de exceções fica mais profundo**: ao depurar, é preciso navegar por mais uma camada de chamada.

### Quando reconsiderar
Se o número de decorators encadeados crescer (ex: adicionar cache, métricas, tracing), considerar Chain of Responsibility ou pipeline explícito.

---

## Event listeners via DatabaseResiliencePipelineBuilder

### A escolha
Pipeline construída em classe dedicada injetada via DI, permitindo que os event listeners usem `WorkerStateTracker` e um logger injetados.

### O que se prioriza
Testabilidade, formatação consistente de logs, fonte única de verdade para estado do Worker.

### Do que se abre mão
- **Configuração inline mais legível**: a forma idiomática mais comum com Resilience4j é anotar métodos com `@CircuitBreaker`/`@Retry` e configurar via `application.yml`, usando um logger estático nos listeners. O builder dedicado é menos comum, mais código.
- **Pipeline configurável por chamador**: cada serviço que quiser uma pipeline diferente precisa de seu próprio builder. Para a V1.2 não é problema porque só temos uma pipeline (database).

### Quando reconsiderar
Se em V2.0+ o middleware tiver múltiplas pipelines (database, MQTT, HTTP), considerar registrar beans de pipeline nomeados e voltar à configuração declarativa.

---

## SyncBatchSize default 10 como minimum viable

### A escolha
Valor default conservador, configurável via `application.yml`.

### O que se prioriza
Segurança: 10 é pouco o suficiente para não sobrecarregar o primário durante recuperação, mesmo em hardware modesto.

### Do que se abre mão
- **Velocidade de recuperação**: queda de 1 hora com 1Hz gera 3600 leituras pendentes. Com batch de 10 e gravação a cada 2 segundos, leva 12 minutos para drenar.
- **Adaptação automática**: o valor é estático. Não se ajusta dinamicamente conforme a saúde do banco ou carga do Worker. Sistema adaptativo seria mais robusto mas exige medição contínua.

### Quando reconsiderar
Em hardware com folga conhecida, aumentar para 50 ou 100 via configuração. Em V1.5+ podemos avaliar implementar adaptive batch sizing baseado em latência média das últimas N gravações.

---

## Buffer nunca deleta (append-only com flag synced)

### A escolha
Leituras sincronizadas ficam com `synced = 1` permanentemente. Não há `DELETE FROM`.

### O que se prioriza
Auditoria, diagnóstico forense de quedas passadas, simplicidade do código (sem políticas de limpeza para errar).

### Do que se abre mão
- **Crescimento ilimitado do arquivo**: em ambientes com quedas frequentes, o `buffer.db` pode chegar a centenas de MB ou GB ao longo de meses. Sem política de retenção, vai consumir disco.
- **Performance de queries no buffer**: tabela grande com maioria de registros já sincronizados torna queries `WHERE synced = 0` cada vez mais lentas se SQLite não tiver índice adequado.

### Quando reconsiderar
Em V1.7+ implementar política de retenção configurável: por tempo (ex: deletar synced=1 mais antigos que 90 dias) ou por tamanho (ex: manter no máximo 100MB de histórico).

---

## VacuumChamberSimulator por fases lineares em vez de modelo físico completo

### A escolha
Simulação por interpolação linear entre fases definidas (ocioso, rampa, hold, rampa, ocioso) com ruído gaussiano.

### O que se prioriza
Simplicidade, dados que parecem realistas o suficiente para apresentação técnica, ciclo determinístico fácil de explicar.

### Do que se abre mão
- **Fidelidade física**: modelo real envolveria lei de Newton de resfriamento, capacidade térmica do meio, perdas radiativas. Para validação contra dados reais de equipamento, a simulação atual é insuficiente.
- **Variabilidade de operação**: o ciclo é sempre igual. Equipamento real tem variações de carga, falhas pontuais, ciclos abortados. A simulação não modela nada disso.
- **Multi-variável**: real seria temperatura, pressão de vácuo, e talvez umidade. Atual simula só temperatura.

### Quando reconsiderar
Quando o objetivo passar de "apresentar o middleware" para "validar comportamento de coleta contra processo real". Aí vale investir em modelo físico ou usar gravações de operação real como playback.

---

## ThreadLocalRandom em vez de Random com seed injetável

### A escolha
`ThreadLocalRandom.current()` no `VacuumChamberSimulator`.

### O que se prioriza
Simplicidade, segurança em ambiente multi-thread e ausência de contenção.

### Do que se abre mão
- **Reproducibilidade**: `ThreadLocalRandom` não aceita seed fixo, então cada execução tem ruído diferente. Para testes determinísticos, seria melhor injetar um `Random` com seed fixo.
- **Injeção**: como é acessado estaticamente, não dá para substituir por um gerador controlado em teste sem refatorar a assinatura.

### Quando reconsiderar
Quando testes automatizados forem implementados (V2.0+), injetar um `Random` via DI para permitir seed fixo em testes.

---

## Health check da Api separado do estado do Collector

### A escolha
A Api verifica apenas conectividade com o banco via `SELECT 1`. Não sabe se o Collector está vivo, em que estado, ou quando foi a última leitura.

### O que se prioriza
Simplicidade da Api, isolamento de processos.

### Do que se abre mão
- **Observabilidade ponta a ponta**: monitoring externo que consulta `/health` recebe `healthy` mesmo se o Collector morreu e nenhuma leitura nova está entrando.
- **Decisão informada**: orquestradores como Kubernetes não conseguem reiniciar o Collector com base no `/health` da Api.

### Quando reconsiderar
V1.5+ com Prometheus: Collector expõe métricas (last_reading_timestamp, current_state). Api ou Prometheus consultam essas métricas. Health check verdadeiramente ponta a ponta.

---

## Sem load test formal antes de produção

### A escolha
A V1.2 não passou por load test estruturado. Capacidade estimada conservadoramente em 3 a 5 dispositivos simultâneos com 1 leitura/segundo cada.

### O que se prioriza
Velocidade de entrega do MVP, foco em funcionalidade e correção antes de otimização prematura.

### Do que se abre mão
- **Garantia de capacidade**: não sabemos onde está o gargalo real. Pode ser o SQLite com lock de banco, o TimescaleDB com inserts sequenciais, ou o ciclo do Worker bloqueando.
- **Justificativa para venda em produção**: cliente real perguntar "quantos dispositivos suporta" não tem resposta defensável atualmente.

### Quando reconsiderar
Antes de qualquer instalação em produção real. Plano: usar k6 ou Artillery para simular múltiplas instâncias de Collector escrevendo simultaneamente, medir CPU, memória, latência de gravação, e identificar gargalo concreto.

---

## Serialização ISO-8601 e BigDecimal-string no buffer

### A escolha
No buffer SQLite, `timestamp` é gravado em ISO 8601 e `value` como string de `BigDecimal`, em vez de depender de tipos nativos do SQLite ou da formatação default do locale.

### O que se prioriza
Robustez cross-locale e precisão exata. O sistema funciona corretamente em máquinas com `pt-BR`, `en-US`, ou qualquer outro locale sem mudança de código, e sem perder dígitos decimais.

### Do que se abre mão
- **Overhead de parse/format**: cada gravação e leitura do buffer converte string ↔ tipo, em vez de usar binário nativo.
- **Risco de esquecer**: novos pontos de serialização que usarem `String.format` com locale ou tipos de ponto flutuante reintroduzem o risco. Padrão de equipe e revisão de código mitigam isso.

### Quando reconsiderar
Nunca para o timestamp e o decimal. Esta é a forma correta. A alternativa de confiar no locale default é frágil e quebra em produção.

---

## Spring JdbcClient em vez de JPA/Hibernate

### A escolha
Acesso a dados com `JdbcClient` e SQL explícito, em vez de um ORM completo (JPA/Hibernate).

### O que se prioriza
Controle total sobre o SQL, previsibilidade de performance, ausência de mapeamento mágico, e proximidade com o modelo de séries temporais do TimescaleDB (hypertables, funções específicas).

### Do que se abre mão
- **Produtividade em CRUD genérico**: um ORM geraria queries de CRUD automaticamente. Aqui cada query é escrita à mão.
- **Portabilidade entre bancos**: SQL explícito acopla mais à dialeto do banco. Para um middleware que mira TimescaleDB especificamente, é aceitável.
- **Cache de primeiro/segundo nível**: recursos que o Hibernate oferece de fábrica precisariam ser implementados manualmente se necessário.

### Quando reconsiderar
Se o domínio de persistência crescer para muitas entidades relacionais com CRUD repetitivo. Para o escopo atual (uma tabela de leituras append-heavy), o ORM seria peso morto.

---

## Eclipse Milo para OPC UA

### A escolha
`OpcUaProtocolReader` usa a stack Eclipse Milo para falar OPC UA.

### O que se prioriza
Milo é a implementação OPC UA de referência no ecossistema Java, ativa e madura, cobrindo cliente e servidor com suporte a segurança (SignAndEncrypt) para evolução futura.

### Do que se abre mão
- **Peso de dependências**: Milo traz transitivamente Netty e bibliotecas de criptografia, aumentando o tamanho do artefato.
- **Curva de aprendizado**: a API expõe conceitos de OPC UA (NodeId, DataValue, StatusCode, sessões) que exigem familiaridade com o protocolo.

### Quando reconsiderar
Apenas se um protocolo industrial diferente (Modbus, MQTT) substituir o OPC UA como principal. Enquanto OPC UA for o alvo, Milo é a escolha consolidada.

---

## Cobertura de testes (unitários, integração e camada web)

### A escolha
Três camadas de teste, separando os rápidos dos que sobem infraestrutura:
- **Unitários** (`*Test`, Surefire): decorator de resiliência (fallback, re-lançamento quando ambos os destinos falham com a causa preservada, sincronização do buffer, item corrompido, parada no primeiro erro), parsing do buffer (round-trip exato, valores e timestamps malformados, independência de locale), pipeline de resiliência (sucesso, esgotamento de retries, recuperação, timeout), simulador e estado do Worker.
- **Integração** (`*IT`, Failsafe): gravação e consulta reais contra um **TimescaleDB em container (Testcontainers)** e contra um **arquivo SQLite real**, exercitando a DDL, o SQL e o mapeamento de tipos.
- **Camada web** (`@WebMvcTest` + MockMvc): `/readings` e `/health` (200 e 503).

Os tempos da pipeline de resiliência são injetáveis (`Tuning`) para permitir testes rápidos sem alterar o comportamento de produção.

### O que se prioriza
Proteger o comportamento crítico (zero perda de dados, resiliência, contrato HTTP) contra regressão. Os unitários são rápidos e determinísticos; os de integração (`*IT`) só rodam em `verify` e são pulados automaticamente onde não há Docker.

### Do que se abre mão
- **Load test**: capacidade sob carga continua não validada (ver seção própria).
- **Cobertura de mutação**: ainda não há análise de mutação (ex.: PIT) para medir a qualidade dos testes além da cobertura de linha.

### Quando reconsiderar
Antes da V2.0: o load test formal e, opcionalmente, testes de mutação para endurecer ainda mais a suíte.
