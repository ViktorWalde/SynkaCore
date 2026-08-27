# Firmware do Nó — C++ Restrito sobre ESP-IDF

**Status:** ACEITA, **não ativa**
**Data:** 2026-08-27
**Ativação:** somente quando o autor do projeto disser explicitamente para migrar do
nó MicroPython. Até lá, este documento é plano, não instrução.

---

## O que está valendo hoje

O nó de produção **ainda não existe**. O que existe é [`no-micropython/`](../no-micropython/),
um ESP32 com DHT11 rodando MicroPython, usado como **banco de testes** para validar o
caminho IoT de ponta a ponta: contrato de fio, lote, contrapressão, retransmissão,
idempotência e ancoragem de tempo.

Ele não é descartável por ser provisório — ele prova que o caminho funciona antes de
alguém escrever firmware embarcado, que é a ponta mais cara de corrigir.

**Nada neste documento deve ser executado sem sinal explícito.**

---

## Quando esta decisão passa a valer

O MicroPython atende com folga para as famílias **lenta** e **de processo** —
temperatura, umidade, pressão, nível, vazão, peso, estado digital. Isso é a maior
parte do que uma planta mede.

Ele **não atende** para a família **rápida**: vibração e corrente exigem centenas a
milhares de amostras por segundo com RMS e fator de crista calculados no próprio nó.
MicroPython é interpretador com coletor de lixo; não é questão de otimização.

O gatilho, portanto, não é "MicroPython é mais fraco". É concreto:

| Gatilho | Ação |
|---|---|
| Entra vibração ou corrente no escopo | Nó C++ é **requisito** para esses canais |
| Nó precisa rodar meses sem reinício com garantia de memória | Nó C++ é recomendado |
| Contagem de pulso em alta taxa | Nó C++ é requisito |
| Só grandezas lentas, com watchdog e buffer em flash | MicroPython pode ir para produção |

**Frota mista é legítima, e o contrato já suporta.** O gateway não distingue as duas:
um nó MicroPython emite `AmostraEscalar` e `TransicaoDigital`; um nó C++ emite também
`AmostraAgregada`. Foi por isso que o contrato foi declarado neutro de linguagem.

---

## A decisão: C++ restrito, não C

O caminho reflexo seria C. Ele foi rejeitado, e vale registrar o raciocínio porque a
primeira versão desta análise **presumiu C sem argumentar**.

### O que decidiu

Todo o desenho deste sistema se apoia numa propriedade:

> **Possuir um `Envelope` é prova de que ele é válido.**

Campos não exportados mais construtor validante. Não existe "validar de novo por
segurança" em camada alguma acima, porque não é possível fabricar um valor inválido.

**C++ expressa isso**: construtor privado, fábrica estática que devolve um resultado.
**C não expressa**: `struct` é `struct`, qualquer um preenche à mão, e a garantia volta
a depender de disciplina de quem escreve.

Seria incoerente o gateway ter essa garantia estruturalmente e o nó — a ponta onde
divergência se corrige com atualização de firmware em campo — não ter.

O segundo argumento é **RAII**. O nó segura socket, contexto TLS, descritor de arquivo
e mutex. Em C, cada um exige liberação manual em *todo* caminho de erro, e é
exatamente aí que vazamento mora. RAII elimina a classe inteira.

### O argumento contra, e por que ele não vence

O único argumento forte para C é a **auditabilidade do invariante de não-alocação**.

Em C, `grep -rn malloc` responde. Em C++, alocação se esconde em código de aparência
inocente: `std::string s = a + b`, `push_back`, `std::function`.

Ele não vence porque o invariante pode ser **imposto em vez de auditado** — ver a
trava 2 abaixo. E imposto é mais forte que greppável.

### O que não pesou

Tamanho de binário. O ESP-IDF **suporta C++ oficialmente**, com exceções e RTTI
**desabilitados por padrão** e recomendação da Espressif de mantê-los assim. A
configuração padrão de C++ no ESP-IDF já é o subconjunto embarcado.

---

## O subconjunto, e como cada regra é travada

Disciplina declarada não sustenta nada. Cada regra abaixo é uma trava real, ou não é
uma regra.

### 1. Exceções e RTTI desligados

`CONFIG_COMPILER_CXX_EXCEPTIONS=n` e `CONFIG_COMPILER_CXX_RTTI=n`, **fixados
explicitamente no `sdkconfig.defaults`** e não deixados no padrão.

O padrão pode mudar entre versões do IDF; um valor fixado não muda sozinho. E fixá-lo
torna a decisão visível a quem abrir o arquivo.

Consequência: falha de domínio é **valor de retorno**, nunca exceção — exatamente como
no gateway. Conteúdo inválido vindo de um sensor defeituoso não é excepcional; é
resultado esperado de um desenho que assume ruído no barramento.

### 2. `operator new` que aborta depois da inicialização

**Esta é a trava central**, e é o que torna C++ aceitável aqui.

```cpp
// Substitui a alocação global. Depois do boot, alocar é defeito de programação.
void* operator new(std::size_t tamanho) {
    if (inicializacao_concluida) {
        // Em teste: aborta e o defeito aparece na bancada.
        // Em campo: alimenta o watchdog e reinicia — reinício deliberado é
        // estratégia aceita, e é infinitamente melhor que fragmentar até a
        // próxima alocação de TLS falhar, semanas depois, sem ninguém entender.
        registrar_violacao_de_alocacao(tamanho);
        abort();
    }
    return heap_caps_malloc(tamanho, MALLOC_CAP_8BIT);
}
```

Isso é **mais forte** que o `grep` do lado C: deixa de ser "ninguém escreveu `malloc`"
e passa a ser "alocar depois do boot derruba o nó **no teste**, não em campo".

É o mesmo padrão que o projeto usa em toda parte — transformar a regra em algo
impossível de violar em silêncio, como `NovoCatalogoDeConteudo` recusando tipo
repetido na inicialização.

### 3. Cabeçalhos banidos, verificados no build

| Banido | Motivo |
|---|---|
| `<string>` | Aloca em quase toda operação |
| `<vector>`, `<map>`, `<set>`, `<deque>` | Alocam ao crescer |
| `<functional>` | `std::function` aloca ao capturar |
| `<sstream>`, `<iostream>` | Alocam, e trazem flash considerável |
| `<exception>`, `<stdexcept>` | Sem sentido com exceções desligadas |

**Permitidos**: `<array>`, `<optional>`, `<span>`, `<type_traits>`, `<cstdint>`,
`<algorithm>` (a parte que não aloca), e `<memory>` **apenas** para `unique_ptr` com
deletor customizado — que é RAII sobre recurso do IDF e não aloca nada.

Verificação: alvo de build que falha se um cabeçalho banido aparecer. Não é revisão de
código; é o build reprovando.

### 4. Capacidade fixa, dimensionada em tempo de compilação

Buffer de envelopes, fila de despacho e área de serialização são `std::array` ou anel
de tamanho fixo. Nada cresce em execução.

O tamanho vem do orçamento declarado, não de estimativa: autonomia alvo × taxa de
amostragem × bytes por envelope.

### 5. Toda identidade de domínio tem construtor privado

```cpp
class IdDoDispositivo {
public:
    // Único caminho de construção. Possuir um IdDoDispositivo é prova de validade.
    static std::optional<IdDoDispositivo> analisar(std::string_view bruto);
    std::string_view texto() const;
private:
    explicit IdDoDispositivo(std::string_view valido);
    std::array<char, kTamanhoMaximo> valor_{};
    std::uint8_t comprimento_{0};
};
```

Sem construtor padrão. Sem construtor público. É a tradução direta do que os campos
não exportados fazem em Go.

### 6. Sem `virtual` no caminho de aquisição

Tabelas virtuais não alocam, então não violam a trava 2 — mas custam flash e uma
indireção por chamada, e o caminho de aquisição não tem variação real que justifique
polimorfismo em execução. Onde houver variação prevista, usa-se template ou ponteiro
de função.

### 7. `-Wall -Wextra -Werror`, e `clang-tidy`

Mesmo princípio do gateway: qualquer aviso derruba o build. Sem aviso "ignorado".

---

## O que vem do contrato, e o que não vem

**Vem gerado**: as estruturas de mensagem, via **nanopb**, do mesmo
`contrato/proto/synkacore/contrato/v1/aquisicao.proto`. O nanopb é feito para alocação
estática, o que serve diretamente à trava 2.

O nanopb gera **C**. Isso não é atrito: C é chamável de C++ com `extern "C"`, e a API
inteira do ESP-IDF é C de qualquer forma — você chama C nos dois cenários.

**Não vem gerado**: os tipos de domínio com construtor privado. Eles envolvem as
estruturas do nanopb, exatamente como os tipos de `internal/dominio/aquisicao` envolvem
o protobuf gerado no gateway.

**O gerador MicroPython é descartável nesse momento.** `ferramentas/geradordenopython`
existe porque nenhum gerador protobuf para MicroPython serve — `uprotobuf` só fala
proto2, `minipb` exige esquema à mão. Com nanopb, o problema desaparece.

O que **não** é descartável é o **teste de fidelidade entre linguagens**
(`internal/contrato/fidelidade`): ele compara byte a byte o que cada ponta produz para
a mesma mensagem. Quando o nó C++ existir, ele entra no mesmo teste.

---

## Quando esta decisão estaria errada

Registrado para poder ser reavaliada sem discussão.

- **Se a disciplina não for mantida.** C++ mal disciplinado é **pior** que C: sem a
  trava 2 e sem a lista de cabeçalhos banidos, a alocação implícita volta e você perde
  a auditabilidade sem ganhar a garantia. A recomendação depende das travas existirem
  desde o primeiro commit, não de serem acrescentadas depois.
- **Se o nó tiver de rodar em hardware muito mais restrito** que um ESP32 — um
  microcontrolador de 8 bits, por exemplo. Aí C volta a ser a escolha.
- **Se aparecer requisito de certificação funcional** (IEC 61508 ou similar) com
  restrição a subconjunto de linguagem. Aí a norma decide, não este documento.

---

## Próximos passos, quando ativado

1. Esqueleto ESP-IDF com `sdkconfig.defaults` fixando exceções e RTTI desligados.
2. Trava 2 — `operator new` que aborta — **antes** de qualquer lógica.
3. Verificação de cabeçalhos banidos no build.
4. nanopb gerando do `.proto`, com o teste de fidelidade estendido ao C++.
5. Tipos de identidade com construtor privado.
6. Portar o laço do nó, que já está provado duas vezes: em Go
   (`internal/no`) e em MicroPython (`no-micropython/main.py`).

O passo 6 é o mais barato dos seis, e não por acaso: o pensamento já foi feito, e é ele
que transfere entre linguagens.
