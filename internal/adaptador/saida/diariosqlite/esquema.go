package diariosqlite

// esquema e o DDL do diario de ingestao.
//
// Embutido no binario, e nao lido de um arquivo ao lado, porque o diario e a
// definicao de durabilidade do sistema: um binario que encontra um .sql faltando
// numa planta as tres da manha nao tem o que fazer. Migracao versionada entra
// quando houver a segunda versao do esquema; ate la, um DDL idempotente e o
// mecanismo mais simples que funciona.
const esquema = `
-- WAL permite que a projecao leia enquanto a ingestao grava, sem que uma bloqueie
-- a outra. Sem ele, um lote de projecao seguraria o caminho de aquisicao — e o
-- caminho de aquisicao e o unico que nunca pode parar.
PRAGMA journal_mode = WAL;

-- FULL, e nao NORMAL. NORMAL deixa a confirmacao da transacao acontecer antes de
-- o dado estar no disco: numa queda de energia — que em planta industrial e
-- rotina, nao excecao — o gateway teria confirmado a remessa para a origem, a
-- origem teria liberado o buffer, e o dado nao existiria em lugar nenhum.
--
-- E o custo esta pago pelo LOTE: com group commit, um fsync serve uma remessa
-- inteira, e a taxa cai para a faixa de dezenas por segundo.
PRAGMA synchronous = FULL;

PRAGMA foreign_keys = ON;

-- diario e APPEND-ONLY no caminho de aquisicao. Nada aqui e atualizado depois de
-- gravado; o avanco da projecao mora em outra tabela, de proposito (ver
-- cursor_de_projecao).
CREATE TABLE IF NOT EXISTS diario (
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,

    -- A restricao de unicidade e o UNICO ponto de deduplicacao do sistema.
    -- Store-and-forward com retransmissao sempre gera entrega duplicada; sem esta
    -- linha, toda parada de maquina seria contada duas vezes.
    chave_de_idempotencia  TEXT    NOT NULL UNIQUE,

    id_do_dispositivo      TEXT    NOT NULL,
    id_da_sessao_de_boot   TEXT    NOT NULL,
    numero_de_sequencia    INTEGER NOT NULL,
    versao_do_esquema      INTEGER NOT NULL,
    tipo_de_conteudo       TEXT    NOT NULL,
    classe_de_dado         TEXT    NOT NULL,

    -- O tempo BRUTO que a origem afirmou. Nunca sobrescrito por nada derivado.
    tempo_ligado_ms        INTEGER NOT NULL,

    -- Quando o gateway recebeu, em ISO-8601 UTC.
    --
    -- TEXT e nao REAL: ponto flutuante perde precisao em instante, e o formato
    -- textual e independente de locale por natureza. Uma gravacao que dependesse
    -- da formatacao local produziria "72,5" numa maquina pt-BR e falharia na
    -- leitura em silencio.
    instante_observado     TEXT    NOT NULL,

    -- Os bytes canonicos do conteudo, exatamente como o codec os produziu.
    --
    -- O dado bruto da origem NUNCA e substituido por uma interpretacao nossa: se a
    -- decodificacao estiver errada, o original ainda permite reprocessar. Campos
    -- que este binario nao conhece sobrevivem aqui e serao lidos pelo proximo.
    conteudo_bruto         BLOB    NOT NULL
);

-- Serve a consulta de lacuna em transito: "qual a maior sequencia ja durável
-- desta sessao?" e "faltou algum numero no meio?".
CREATE INDEX IF NOT EXISTS idx_diario_sessao_sequencia
    ON diario (id_do_dispositivo, id_da_sessao_de_boot, numero_de_sequencia);

-- cursor_de_projecao guarda ate onde cada consumidor ja projetou.
--
-- Cursor, e nao uma coluna "projetado" no proprio diario, por tres razoes:
--
--   1. O diario continua append-only. Marcar linha exigiria um UPDATE por
--      registro projetado, dobrando a escrita no caminho mais quente do sistema.
--   2. Permite MAIS DE UM consumidor com avancos independentes, sem uma coluna
--      nova por consumidor.
--   3. Retomada apos queda e uma leitura, nao uma varredura: o cursor diz
--      exatamente onde continuar, e reprocessar a partir dali e idempotente.
CREATE TABLE IF NOT EXISTS cursor_de_projecao (
    nome                 TEXT    PRIMARY KEY,
    ultimo_id_projetado  INTEGER NOT NULL,
    atualizado_em        TEXT    NOT NULL
);

-- ancora_de_sessao_de_boot amarra o tempo monotonico de cada sessao ao relogio do
-- gateway, e PRECISA sobreviver ao reinicio do gateway.
--
-- Se a ancora vivesse so em memoria, reiniciar o gateway a reconstruiria a partir
-- da proxima mensagem recebida — e a origem, que nao reiniciou, continuaria
-- reportando o mesmo tempo monotonico crescente. A nova ancora atribuiria a esse
-- tempo um instante de parede diferente, e TODA a serie daquela sessao passaria a
-- ser derivada com um deslocamento. O dado resultante seria plausivel, que e o
-- pior desfecho possivel.
--
-- A chave primaria composta e a propria identidade da sessao: uma ancora por
-- (dispositivo, sessao de boot), imutavel depois de criada.
CREATE TABLE IF NOT EXISTS ancora_de_sessao_de_boot (
    id_do_dispositivo       TEXT    NOT NULL,
    id_da_sessao_de_boot    TEXT    NOT NULL,

    -- O tempo ligado informado na PRIMEIRA mensagem da sessao.
    tempo_ligado_da_ancora_ms INTEGER NOT NULL,

    -- O relogio de PAREDE do gateway naquele instante, em ISO-8601 UTC.
    instante_da_ancora      TEXT    NOT NULL,

    -- A leitura MONOTONICA do gateway no mesmo instante, em nanossegundos.
    --
    -- Guardada junto da parede porque as duas juntas sao o que torna um degrau de
    -- relogio detectavel. Ela e relativa a partida do PROCESSO, entao perde
    -- sentido apos um reinicio — e por isso a coluna abaixo existe.
    decorrido_da_ancora_ns  INTEGER NOT NULL,

    -- Identificador da execucao do processo que criou a ancora.
    --
    -- Sem ele, uma leitura monotonica gravada por um processo anterior seria
    -- comparada com a do processo atual, e a diferenca entre duas origens de
    -- contagem sem relacao apareceria como um degrau de relogio gigante e falso.
    -- Com ele, o gateway sabe quando a comparacao monotonica NAO se aplica e
    -- limita a verificacao ao que ela pode de fato afirmar.
    id_da_execucao          TEXT    NOT NULL,

    criada_em               TEXT    NOT NULL,

    PRIMARY KEY (id_do_dispositivo, id_da_sessao_de_boot)
);
`
