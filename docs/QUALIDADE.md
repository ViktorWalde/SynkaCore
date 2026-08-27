# SynkaCore — Garantias de Qualidade

O build é uma sequência de portões. Qualquer violação derruba o build: **disciplina é
imposta pela ferramenta, não pela boa vontade**. Tudo roda com `make verificar`.

Este princípio veio da V1.x e é a única coisa da esteira de qualidade que atravessou a
reescrita sem mudar. As ferramentas são outras; a regra é a mesma — *se algo só vale quando
alguém lembra, será quebrado*.

---

## O portão completo

```bash
make verificar
```

| Etapa | O que faz |
|---|---|
| `formatar-conferir` | Falha se algum arquivo divergir do `gofmt` |
| `vet` | Análise do próprio toolchain do Go |
| `testar` | `go test -race -count=1 ./...` |
| `linter` | `golangci-lint run` |
| `contrato-conferir` | Falha se o código gerado estiver desatualizado em relação ao `.proto` |

---

## Exaustividade sobre enum — o portão mais importante

`exhaustive`, com **`default-signifies-exhaustive: false`**.

Essa configuração é o ponto inteiro. Com o padrão (`true`), qualquer `switch` com `default`
passaria — e o linter deixaria de verificar exatamente o que ele existe para verificar.

Os `switch` sobre `ClasseDeDado`, `EstadoDeMaquina` e `falha.Categoria` **não têm cláusula
`default`**. Acrescentar um membro ao enum reprova o build em todo ponto que exige uma
decisão.

> Registrado porque o erro é fácil de cometer: uma versão anterior desse desenho tinha
> `default` e a documentação o descrevia como se fosse uma trava. Não era — o caso novo cairia
> no padrão em silêncio, e a classe de dado mais crítica do sistema entraria pela porta que o
> próprio autor deixou aberta.

Onde há retorno após o `switch`, ele é alcançável apenas com valor inválido — que o
construtor já recusa — e sempre erra **para o lado de preservar dado**: guardar algo
descartável custa espaço; descartar algo garantido custa o dado.

---

## Não-duplicação, verificada mecanicamente

Documentar "não duplique" não sustenta nada: revisor cansado aprova função repetida, e a
repetição só aparece quando as duas cópias já divergiram.

| Linter | O que pega |
|---|---|
| `dupl` | Blocos parecidos — a duplicação literal |
| `goconst` | Literal repetido: primeiro sintoma de uma regra prestes a ser reescrita em dois lugares e ficar diferente nos dois |
| `gocognit` | Função que incha é função que vai ser copiada |
| `nestif` | Aninhamento que esconde caminho não testado |

### Uma exclusão, e o argumento dela

`dupl` é excluído em `internal/dominio/aquisicao/conteudo*.go`, e vale explicar por quê —
excluir sem argumento seria desligar o portão.

O esqueleto que de fato se repetia — alocar a mensagem do contrato, decodificar, envolver o
erro — **foi extraído** para `definirConteudo`. O que sobra em cada arquivo é um
identificador de tipo, uma estrutura de dois ou três campos e a conversão daquele conteúdo
específico.

Unificar o que restou exigiria um tipo genérico de conteúdo, e ele apagaria exatamente o que
o sistema precisa distinguir: `AmostraEscalar` e `LeituraDeContador` têm a **mesma forma** e
requisitos **opostos**. É dessa distinção que saem as cinco políticas de `ClasseDeDado`.

E a garantia contra divergência ali não vem do `dupl` — vem de uma trava mais forte, descrita
abaixo.

### Uma exclusão que não é exclusão: `misspell` fica de fora

`misspell` é um corretor de **inglês**, e a documentação do projeto é em português. Ligado,
ele acusava `eles` como erro de *eels* e `processos` como erro de *processors* — dezenas de
falsos positivos.

Um portão que produz ruído treina o leitor a ignorar a saída do linter inteiro, e aí todos os
outros portões param de funcionar junto. Ele sai.

---

## Travas no próprio código

Os linters cobrem a forma. As travas abaixo cobrem o significado, e são mais fortes porque
não dependem de configuração de ferramenta.

### O catálogo cobre o contrato, verificado por reflexão

`TestTodoConteudoDoContratoTemDefinicao` lê o descritor do protobuf e confere, nos **dois
sentidos**:

- todo campo do `oneof conteudo` tem uma definição de catálogo que o interpreta;
- toda definição de catálogo corresponde a um campo do contrato.

Sem isso, acrescentar uma mensagem ao contrato e esquecer de ensinar o gateway a
interpretá-la produziria "tipo desconhecido" **em campo, numa planta** — e não aqui.

A lista de tipos vive em `aquisicao.TodasAsDefinicoes`, e não na raiz de composição, para que
o teste leia a **mesma** lista. Duas listas do mesmo conjunto divergem, e a que o autor
esquece de atualizar é sempre a do teste.

> Verificado empiricamente: removendo um tipo do inventário, o teste reprova com
> `o contrato declara o conteudo "lacuna_de_buffer", mas nenhuma definicao de catalogo o interpreta`.

### O catálogo recusa duplicata na inicialização

`NovoCatalogoDeConteudo` rejeita tipo repetido. Dois arquivos definindo o mesmo tipo derrubam
o gateway **no boot**, não em produção.

### Um ponto de validação por conceito

`NovoEnvelope` é o único construtor de mensagem. Campos não exportados mais construtor
validante significam que **possuir um `Envelope` é prova de que ele é válido** — e a
consequência é que não existe "validar de novo por segurança" em camada alguma acima.

Essa é a origem mais comum de validação divergente: duas checagens da mesma coisa que
discordam depois de seis meses.

### O contrato gerado está em dia

`contrato-conferir` regera o código a partir do `.proto` e falha se o resultado diferir do
versionado. Sem essa trava, alguém edita o contrato, esquece de regerar, e o binário passa a
falar uma versão que não é a documentada — divergência que só aparece quando uma origem em
campo manda o campo novo.

---

## Testes

`go test -race -count=1 ./...`, sem exceção.

`-count=1` desliga o cache: um teste que passa por estar em cache não passou. `-race` é
obrigatório porque o nó roda dois laços concorrentes sobre um buffer compartilhado.

> O `-race` exige cgo, e o build de produção usa `CGO_ENABLED=0` para produzir binário
> estático. O Makefile define a variável **por alvo**, e não globalmente. A alternativa —
> largar o `-race` para manter uma variável arrumada — seria trocar verificação de
> concorrência por conveniência de build.

### Testes contra o real, não contra dublês

O diário usa **arquivo temporário de verdade** (`t.TempDir()`), nunca `:memory:` nem um
dublê. O diário *é* a definição de durabilidade do sistema, e um dublê testaria a nossa ideia
do SQLite em vez do SQLite. Nesta escala, o arquivo real não é mais lento.

Onde há dublê, ele existe por uma razão nomeada: `Transportador` é interface para que os três
cenários que decidem se o nó funciona — gateway fora, contrapressão e retomada — sejam
exercitáveis. São os mais caros de reproduzir com rede de verdade, e sem o dublê ficariam sem
exercício.

O mesmo vale para `relogio.Falso`: `DarDegrau` move só o relógio de parede, que é o que um
acerto de hora faz. Um degrau real é impossível de reproduzir em integração contínua.

---

## O teste que existe por causa de um defeito real

`TestAmostragemNaoParaQuandoOGatewayCai` merece nota, porque a história dele diz mais que o
código.

O teste de ponta a ponta encontrou o nó **parando de amostrar por 15 segundos** durante uma
queda de 12 segundos do gateway: amostragem e despacho dividiam um `select`, e o recuo do
despacho — que dorme — bloqueava o temporizador.

**E o primeiro teste de regressão não pegava o defeito.** Ele verificava contiguidade de
sequência, e passava com o defeito reintroduzido — porque o número de sequência é atribuído
no enfileiramento: um amostrador travado produz **menos** amostras, todas perfeitamente
contíguas. A contiguidade é cega para uma parada de amostragem.

O instrumento certo é o espaçamento entre os **tempos ligados**, que vêm do relógio monotônico
no instante da medição. O teste corrigido reprova com:

```
maior intervalo entre amostras = 108ms, acima do limite de 30ms (nominal 10ms)
```

A lição vale mais que a correção: **um teste que passa não prova que ele testa o que você
acha que ele testa.** Vale reintroduzir o defeito e conferir que o teste reprova — foi assim
que este ficou correto.

---

## Segurança

`gosec`, com uma exclusão e três supressões pontuais, cada uma com o motivo no código.

- **G115** (conversão entre larguras de inteiro) é excluído globalmente, porque a checagem
  que importa é feita à mão e testada: o envelope valida o tempo ligado **na largura em que o
  dado chegou**, antes de qualquer conversão. Um nó hostil enviando `1<<62` ms estouraria o
  `int64` e envolveria para um valor pequeno e plausível — a validação passaria e o quadro
  corrompido entraria no sistema. Há teste para os três casos hostis.
- **G404** (gerador aleatório fraco) é suprimido em três pontos, todos com justificativa
  local: são ruído de simulação e *jitter* de recuo. Onde a imprevisibilidade importa de
  fato — o identificador de sessão de boot, que compõe a chave de idempotência —
  `identificador.Sortear` usa `crypto/rand`.

Além disso, no caminho de aquisição: limite de corpo imposto **antes** da leitura, limite de
envelopes por remessa, limite de profundidade de recursão do protobuf, cópia defensiva do
buffer do adaptador, e nenhuma mensagem interna de erro devolvida pela rede — ela vai para o
log, onde o operador a lê, e não para um atacante mapeando o que o gateway valida.

---

## Corretude

| Linter | O que pega |
|---|---|
| `errorlint` | Comparação de erro sem `errors.Is`/`As` |
| `bodyclose` | Corpo de resposta HTTP não fechado |
| `nilerr` | Retornar `nil` depois de checar um erro |
| `sqlclosecheck` | `Rows` não fechado |
| `makezero` | `make` com tamanho seguido de `append` |
| `copyloopvar` | Captura de variável de laço |

---

## Higiene

`unconvert`, `wastedassign`, `prealloc`.

---

## Onde a configuração vive

| Arquivo | O quê |
|---|---|
| `Makefile` | Os portões e a ordem deles |
| `.golangci.yml` | Linters, ajustes e as exclusões, cada uma com o argumento |
| `go.mod` | Versão da linguagem e dependências |
