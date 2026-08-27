# SynkaCore — Trade-offs Arquiteturais

Documento honesto sobre as decisões que abrem mão de algo para priorizar outra coisa. Nenhuma
arquitetura é perfeita, e cada escolha tem custo. Este documento existe para que o custo seja
explícito, e para que decisões futuras tenham contexto sobre o porquê das atuais.

> Os trade-offs da implementação Java V1.x estão preservados em
> [`legado/java-v1.2/`](../legado/java-v1.2/) junto com o código. Vários deles foram
> **resolvidos** pela reescrita, e onde isso aconteceu está anotado abaixo.

---

## O diário como caminho primário, e não como emergência

### A escolha

Toda remessa vai para um diário SQLite local **antes de qualquer confirmação**. Um projetor
assíncrono alimenta o TimescaleDB depois.

### O que se prioriza

Que "zero perda" deixe de ser uma promessa e vire consequência estrutural. Não existe caminho
de exceção a manter funcionando, porque não existe caminho de exceção.

### Do que se abre mão

- **Latência até o dashboard.** O dado aparece no Grafana com o atraso do ciclo de projeção
  (2 s de padrão, mais o lote). Na V1.x ele aparecia na hora, porque a escrita era direta.
- **Escrita dupla.** O mesmo dado é gravado duas vezes: no diário e no banco de consulta.
  Custa I/O e disco.
- **Um componente a mais para operar.** O projetor tem estado próprio (o cursor) e pode
  ficar para trás sem que nada mais pareça errado — daí `/saude` reportar os dois estágios
  separados.

### Quando reconsiderar

Se surgir requisito de latência **fim a fim** abaixo de um segundo até a tela. Aí valeria uma
escrita direta em paralelo à projeção — mas só depois de reconhecer que isso reintroduz o
caminho duplo que esta decisão eliminou.

---

## SQLite em Go puro, e não `mattn/go-sqlite3`

### A escolha

`modernc.org/sqlite`, que é SQLite transpilado para Go.

### O que se prioriza

O binário verdadeiramente estático. `mattn/go-sqlite3` usa cgo, é o SQLite em C de verdade,
e **quebra a propriedade que justificou a linguagem**: implantar deixaria de ser copiar um
arquivo.

### Do que se abre mão

- **Velocidade do driver.** O transpilado é mais lento que o C nativo.
- **Proximidade do upstream.** Uma correção do SQLite chega depois, via retranspilação.

### Por que o custo não morde

Com lote e *group commit*, o diário faz **dezenas de transações por segundo**, não milhares.
Desempenho de driver é irrelevante nessa faixa. Ganhar velocidade que não falta pagando com a
única propriedade que importa seria o pior negócio possível.

### Quando reconsiderar

Se a carga subisse a ponto de o driver aparecer num perfil de CPU. Hoje o gargalo é o
`fsync`, e ele é do disco, não do driver.

---

## `synchronous = FULL` no diário

### A escolha

O SQLite espera o dado chegar ao disco antes de confirmar a transação.

### O que se prioriza

Que a confirmação ao nó signifique o que ela diz. Com `NORMAL`, a transação confirma antes de
o dado estar no disco: numa queda de energia — que em planta industrial é rotina, não exceção
— o gateway teria confirmado, o nó teria liberado o buffer, e o dado não existiria em lugar
nenhum.

### Do que se abre mão

- **Taxa de transações.** `FULL` custa um `fsync` por transação.

### Por que o custo está pago

Pelo **lote**. Um `fsync` serve uma remessa inteira. Sem lote, o `fsync` sozinho consumiria
toda a capacidade do disco no cenário dimensionado; com lote de 100, cai para a faixa de 1%.

É por isso que o lote não é otimização — é pré-requisito de viabilidade, e é o que torna esta
decisão barata.

---

## Uma conexão de escrita no diário

### A escolha

`SetMaxOpenConns(1)`.

### O que se prioriza

Eliminar uma classe inteira de defeito. SQLite permite exatamente um escritor por vez; com
várias conexões, o excedente não ganha paralelismo — ganha `SQLITE_BUSY`, que é um modo de
falha a tratar em vez de uma condição a evitar.

### Do que se abre mão

- **Leitura concorrente com escrita.** O projetor lê pela mesma conexão que a ingestão usa
  para gravar, então eles se serializam. O WAL já permitiria separá-los.

### Quando reconsiderar

Quando medição real mostrar a projeção competindo com a ingestão. Fazer isso agora seria
otimizar por intuição um recurso que está praticamente ocioso.

---

## Cursor de projeção, e não coluna `projetado` no diário

### A escolha

O avanço da projeção vive numa tabela própria; o diário é append-only.

### O que se prioriza

Três coisas ao mesmo tempo: o diário permanece append-only (marcar linha exigiria um `UPDATE`
por registro projetado, dobrando a escrita no caminho mais quente); mais de um consumidor
pode avançar independentemente sem uma coluna nova por consumidor; e a retomada após queda é
uma leitura, não uma varredura.

### Do que se abre mão

- **Saber, olhando uma linha, se ela já foi projetada.** A resposta exige comparar o `id`
  com o cursor.

### Comparação com a V1.x

A V1.x usava uma flag `synced` no buffer e **nunca deletava**, o que os próprios trade-offs
da época registravam como problema: o arquivo cresceria a centenas de MB ou GB sem política
que o contivesse, e consultas `WHERE synced = 0` ficariam progressivamente mais lentas.
Resolvido: ver a poda, abaixo.

---

## Poda com duas condições, e não uma

### A escolha

Um registro só sai do diário se estiver **abaixo de todos os cursores** *e* for mais antigo
que a janela de retenção (7 dias de padrão).

### O que se prioriza

As duas condições cobrem falhas opostas, e nenhuma sozinha basta:

- Só a idade apagaria dado que a projeção ainda não consumiu, durante uma parada prolongada
  do banco — exatamente o cenário em que o diário é a única cópia existente.
- Só o cursor apagaria o dado assim que projetado, e um erro descoberto na projeção seria
  irrecuperável.

### Do que se abre mão

- **Disco.** Sete dias de dado bruto ficam retidos mesmo depois de projetados.

### Quando reconsiderar

A janela é parâmetro. Hardware com pouco disco reduz; instalação com exigência regulatória
mais dura aumenta.

---

## Modelo de leitura estreito, e não uma coluna JSONB

### A escolha

Uma linha por **campo projetado**, com três colunas de valor: numérico, texto e lógico.

### O que se prioriza

Que a série temporal comprima de verdade, que o Grafana consulte sem conhecer a forma interna
de cada tipo de conteúdo, e que o esquema não possa ganhar tipos por acidente — as três
colunas são exatamente os três tipos que a interface selada `ValorProjetado` admite.

"Modelagem genérica de eventos temporais" na prática vira uma coluna JSONB onde tudo cabe e
nada é validado.

### Do que se abre mão

- **Linhas por envelope.** Um envelope com seis campos vira seis linhas. O volume de linhas é
  maior que numa tabela larga.
- **Consulta de um envelope inteiro.** Reunir todos os campos de um envelope exige agregação
  ou pivô.
- **Campos novos exigem migração** se algum dia precisarem de um quarto tipo de valor — o que
  é deliberado: a interface é selada justamente porque este esquema é contrato publicado.

### Quando reconsiderar

Se as consultas dominantes passarem a ser "todos os campos de um envelope" em vez de "um
campo ao longo do tempo". Hoje é o contrário.

---

## Protobuf no fio, em vez de JSON

### A escolha

Um `.proto` versionado como fonte única, do qual todas as pontas são geradas.

### O que se prioriza

Um codec só para todos os transportes; evolução de esquema como ponto forte, com campos
opcionais permitindo gateway e frota em versões diferentes; e cobertura da ponta mais cara
de corrigir — o firmware embarcado, onde divergência de contrato se corrige com atualização
em campo.

### Do que se abre mão

- **Legibilidade no `tcpdump`.** O tráfego deixa de ser inspecionável a olho.
- **Uma ferramenta a mais no build.** `protoc` e `protoc-gen-go` para regerar.
- **Depuração exige um passo.** Ler uma remessa capturada precisa de decodificação.

### Mitigação, e o que ela custa

O código gerado é **versionado**, então `go build` puro funciona sem `protoc` — importante
numa planta sem internet. O custo é que o gerado pode ficar desatualizado em relação ao
`.proto`; por isso `make contrato-conferir` reprova o build nesse caso.

---

## Tipo de conteúdo descoberto por reflexão, e não por `switch`

### A escolha

O codec lê o descritor do protobuf para saber qual conteúdo o envelope carrega. O nome do
campo do `oneof` **é** o identificador do tipo no domínio.

### O que se prioriza

Que não exista uma segunda lista de tipos. Um `switch` no codec seria a segunda lista — a
primeira é `TodasAsDefinicoes` — e duas listas do mesmo conjunto divergem.

### Do que se abre mão

- **Verificação em tempo de compilação.** Um `switch` com `exhaustive` reprovaria o build ao
  acrescentar um tipo; a reflexão descobre em execução.
- **Custo de execução.** A reflexão custa mais que um `switch`.

### Como o custo é contido

O descritor do `oneof` é resolvido **uma vez**, na inicialização do pacote, e não a cada
mensagem. E a verificação em tempo de compilação é substituída por
`TestTodoConteudoDoContratoTemDefinicao`, que confere nos dois sentidos e reprova o build.

---

## Dois laços independentes no nó

### A escolha

Amostragem e despacho em goroutines separadas, comunicando por um buffer com mutex.

### O que se prioriza

Que a amostragem tenha **período fixo garantido por temporizador** e nunca dependa da rede.
Uma série amostrada em intervalos irregulares não é comparável consigo mesma, e nenhuma
análise posterior a conserta.

### Do que se abre mão

- **Simplicidade.** Um `select` único seria mais fácil de ler.
- **Sincronização.** O buffer precisa de mutex.

### Por que não há escolha aqui

A primeira versão era um `select` único, e custou 15 segundos sem nenhuma medição durante uma
queda de 12 segundos do gateway. O recuo do despacho dorme, e dormindo bloqueava o
temporizador. Dado que nunca foi medido não está em buffer nenhum.

---

## Capacidade do buffer do nó em itens, e não em bytes

### A escolha

`CapacidadeDoBuffer` conta itens.

### O que se prioriza

Simplicidade e previsibilidade. Numa origem embarcada, o limite que importa é o número de
estruturas alocadas estaticamente. Um limite em bytes exigiria medir cada serialização antes
de decidir se cabe, pagando esse custo em todo item que caberia sem problema.

### Do que se abre mão

- **Previsibilidade de memória.** Itens de tamanhos muito diferentes fazem o consumo real
  variar. Um descritor de origem é bem maior que uma amostra escalar.

### Mitigação

`BytesEstimados` é reportado na telemetria de saúde, então a ocupação real é observável — e
alarmável **antes** de saturar.

---

## Identificadores em português

### A escolha

Pastas, pacotes, tipos, funções e variáveis em português sem acento.

### O que se prioriza

Linguagem ubíqua aplicada de fato. O vocabulário do domínio já é português em toda a
documentação — "ponto de medição", "sessão de boot", "parada de máquina". Com identificadores
em inglês, todo leitor faria tradução mental constante entre o que os documentos dizem e o
que o código diz.

### Do que se abre mão

- **Convenção do ecossistema.** Go escreve em inglês, e quem chega de fora estranha.
- **Ferramentas que assumem inglês.** `misspell` teve de sair da esteira por produzir dezenas
  de falsos positivos.
- **Mistura inevitável.** Trechos ficam bilíngues, porque três categorias permanecem em
  inglês: o que a linguagem impõe (`main`, `Error`, `String`), o que o compilador reconhece
  (`internal/` **não é escolha** — o Go impõe que pacotes ali não sejam importáveis de fora)
  e o que sai do processo (rótulo de métrica e coluna de banco, consumidos por Prometheus,
  Grafana e SQL).

### Quando reconsiderar

Se o projeto passar a receber contribuição de fora do domínio de língua portuguesa. Aí o
custo do estranhamento passaria a valer mais que o ganho da linguagem ubíqua.

---

## Sem framework HTTP

### A escolha

`net/http` da biblioteca padrão, com o roteamento por padrão de método e caminho do Go 1.22+.

### O que se prioriza

Zero dependência no caminho mais exposto do sistema, e nenhuma camada entre o handler e o que
de fato acontece na conexão.

### Do que se abre mão

- **Middlewares prontos.** Registro de acesso, métricas e autenticação precisam ser escritos.
- **Roteamento avançado.** Grupos, parâmetros tipados e afins não existem.

### Por que o custo é baixo aqui

São **quatro rotas**, todas simples, e duas delas em servidores diferentes por decisão de
topologia. Um framework aqui traria mais superfície do que economia.

---

## Sem TLS ainda no ingresso

### A escolha, e é uma dívida declarada

O ingresso fala HTTP puro hoje.

### O que se prioriza

Fechar primeiro o caminho de dado, que é o que decide se o sistema serve para alguma coisa.

### Do que se abre mão

Tudo o que mTLS entregaria: identidade por dispositivo, confidencialidade e a conferência
entre identidade **reivindicada** e **autenticada** — sem a qual uma origem legítima pode
enviar dados se passando por outra.

### O que já está preparado

O contrato carrega a identidade reivindicada, e o codec **deliberadamente não a confere** —
essa decisão pertence ao adaptador de ingresso, que é quem terá acesso à credencial. A peça
que falta é o adaptador, não o desenho.

### Quando pagar

Antes de qualquer instalação fora de um ambiente controlado. Está registrado como V2.1.

---

## Capacidade ainda não validada sob carga

### A escolha

A reescrita priorizou corretude e o fechamento do caminho de dado.

### Do que se abre mão

- **Saber onde está o gargalo real.** A hipótese é o `fsync` do diário, e ela não foi medida.
- **Resposta defensável a "quantos dispositivos suporta".**

### O que já se sabe

O teste de ponta a ponta rodou um nó a 2 Hz com quatro canais atravessando uma queda de 14
segundos do gateway, sem perda, sem duplicata e sem lacuna de amostragem. Isso valida o
**comportamento**, não a **capacidade**.

### Quando pagar

Antes de qualquer instalação em produção. Herdado como pendência da V1.x, e continua
pendente — dito aqui em vez de omitido.
