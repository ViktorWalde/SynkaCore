# SynkaCore — Propriedade Intelectual e Anterioridade

Documento operacional sobre proteção do código e comprovação de autoria. Não é parecer
jurídico — é o registro do que existe, do que foi feito e do que precisa ser verificado com
quem tem competência para isso.

---

## O ponto de partida: você já tem o direito

Pela **Lei 9.609/1998** (Lei do Software), programa de computador é protegido por **direito
autoral**, e a proteção nasce **automaticamente com a criação**. Não depende de registro,
de aviso de copyright, nem de qualquer formalidade.

O registro no INPI **não cria** o direito. Ele produz **prova de autoria e de data** oponível
a terceiros — que é justamente o que interessa aqui.

Prazo de proteção: **50 anos**, contados de 1º de janeiro do ano seguinte ao da publicação
ou, na ausência dela, da criação.

---

## O que já ancora a anterioridade hoje

Em ordem de força probatória:

| Evidência | Força | Observação |
|---|---|---|
| **Submissão ao SENAI** | Alta | Carrega data institucional independente, produzida por terceiro. É a âncora mais forte que já existe. |
| **Registro no INPI** | Alta | Não existe ainda. Ver abaixo. |
| **Push para remoto** (GitHub/GitLab) | Média | O provedor registra a data de recebimento, fora do seu controle. |
| **Commits do git** | Corroborante | `a2983f8`, de 2026-06-28. Datas de commit são **definidas pelo autor** e o histórico pode ser reescrito. É evidência que apoia as outras, não prova isolada. |

### O que a pasta `legado/java-v1.2/` faz, e o que ela não faz

Ela **não** é prova de data. Ela é **legibilidade**: permite que um avaliador abra o código
anterior sem terminal, sem git e sem conhecimento de ferramenta.

Isso importa porque quem avalia um convênio de fomento raramente vai investigar histórico de
repositório. Ver 38 arquivos Java com documentação de versão datada comunica de imediato o
que um `git log` comunicaria só a quem souber lê-lo.

Ver [`legado/java-v1.2/README.md`](../legado/java-v1.2/README.md).

---

## O manifesto criptográfico

```bash
make manifesto            # gera MANIFESTO.txt
make manifesto-conferir   # confere se o código atual bate com o registrado
```

Produz o SHA-256 de cada arquivo versionado e um **hash raiz** sobre a lista.

### Por que ele existe

O registro no INPI **não publica o código-fonte**. Você deposita o *resumo digital* da
documentação técnica; o código fica com você. O hash é o que permite provar, depois, que o
que você tem em mãos é exatamente o que foi registrado.

Serve também fora do registro formal: um hash depositado numa data não pode ser refeito
depois. Ele é a âncora de conteúdo que o git, sozinho, não fornece.

### Como o determinismo é garantido

Três decisões, cada uma cobrindo um jeito de o resultado variar sem o código mudar:

- **A lista vem do git**, não de um `find`. Um `find` traria `bin/`, `diario/` e artefatos de
  build, que variam por máquina e por momento.
- **Ordenação em `LC_ALL=C`.** Sem isso, a mesma árvore ordenaria diferente em `pt_BR` e
  `en_US`, e o hash raiz mudaria sem nenhuma alteração de código.
- **O `MANIFESTO.txt` fica fora da própria lista.** Sem essa exclusão ele se incluiria, e
  gerar o manifesto alteraria a árvore que o manifesto descreve — a conferência nunca
  fecharia.

> A linha `gerado:` dentro do arquivo é a data da execução e **não prova nada sozinha**: o
> relógio do sistema é ajustável. O que ancora a data é o registro externo onde o hash raiz
> for depositado. Isso está dito dentro do próprio manifesto, para não induzir quem o ler.

---

## O registro no INPI, na prática

### O que é preciso ter antes

1. **Conta no gov.br** em nível prata ou ouro, ou **e-CPF/e-CNPJ**.
2. **Cadastro no e-INPI** — o portal de serviços.
3. **GRU paga** antes de abrir o pedido. O pedido só é aceito com o comprovante vinculado.
4. **Documentação técnica** do programa, da qual se extrai o resumo digital.

### O caminho

O serviço é o **e-Software**, dentro do portal do INPI. O fluxo geral é: emitir e pagar a
GRU → preencher o formulário eletrônico → informar o resumo digital da documentação →
protocolar.

### O que verificar no portal, e não presumir

Estes pontos mudam entre versões do sistema, e chutar um número aqui seria pior que não
dizer nada:

- **Código de serviço da GRU** para registro de programa de computador, e o valor vigente
  (há redução para pessoa física, microempresa, ME/EPP e instituições de ensino — confirme
  se você se enquadra).
- **Algoritmo de resumo digital exigido** e o formato em que ele deve ser informado. O
  `make manifesto` usa SHA-256; se o INPI exigir outro, o alvo é uma linha para ajustar.
- **Formato da documentação técnica** — trechos do código, escopo, e como ela deve ser
  organizada e lacrada.

### O que declarar sobre autoria e titularidade

Duas coisas distintas, e confundi-las é o erro mais caro:

- **Autor** — quem escreveu. É você, e isso não se transfere.
- **Titular** — quem detém os direitos patrimoniais. Pode ser outra pessoa ou entidade.

---

## O ponto que precisa de atenção: a bolsa do SENAI

**Leia o termo da bolsa antes de registrar.**

Convênios de pesquisa, desenvolvimento e inovação frequentemente contêm cláusula de
titularidade sobre o que é desenvolvido durante a vigência — às vezes cotitularidade, às
vezes cessão. Isso não é irregularidade; é cláusula comum e negociada.

O que ela **não** alcança é o que já existia antes. E é exatamente por isso que a separação
entre as duas eras do projeto importa mais do que parecia:

| | Período | Situação |
|---|---|---|
| **V1.0–V1.2** (Java) | Anterior à bolsa | `legado/java-v1.2/`, commit `a2983f8` de 2026-06-28 |
| **V2.0** (Go) | Durante a vigência | Pode estar alcançada pela cláusula, dependendo do termo |

Se houver cláusula de titularidade, ela muda **quem** consta no registro — não **se** o
registro deve ser feito. Registrar sem verificar isso pode gerar um pedido que precise ser
corrigido depois.

Isto é uma questão para quem redigiu o termo ou para assessoria jurídica. Não é decisão
técnica, e não deve ser tomada com base neste documento.

---

## Procedimento recomendado

1. **Ler o termo da bolsa**, especificamente as cláusulas de propriedade intelectual e
   titularidade.
2. **Congelar um marco.** Rodar `make manifesto`, commitar o resultado e criar uma tag
   anotada. O hash raiz passa a identificar aquele estado exato do código.
3. **Fazer push para um remoto**, se ainda não houver. É a âncora externa mais barata que
   existe, e o provedor registra a data de recebimento.
4. **Verificar no e-INPI** os itens listados acima (GRU, algoritmo, formato).
5. **Protocolar**, com a titularidade que o passo 1 determinar.

Os passos 2 e 3 valem a pena mesmo que o registro formal seja adiado ou descartado. Custam
minutos e produzem evidência que não pode ser fabricada depois.
