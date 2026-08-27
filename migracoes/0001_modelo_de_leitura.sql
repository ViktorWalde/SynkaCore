-- Modelo de leitura do SynkaCore — o que o Grafana e as consultas analiticas veem.
--
-- Este NAO e o registro autoritativo. O autoritativo e o diario SQLite local, que
-- guarda os bytes brutos da origem; esta tabela e uma PROJECAO dele, e pode ser
-- reconstruida inteira a partir dali. A distincao importa porque libera decisoes:
-- reter menos aqui, agregar, ate recriar do zero apos um erro de projecao.

CREATE EXTENSION IF NOT EXISTS timescaledb;

-- leitura e estreita de proposito: UMA LINHA POR CAMPO PROJETADO.
--
-- A alternativa seria uma coluna JSONB onde tudo cabe. Ela e recusada porque
-- "modelagem generica de eventos temporais" na pratica vira uma gaveta onde nada e
-- validado, nao comprime como serie temporal, e obriga toda consulta a saber a
-- forma do documento — o que reintroduz, dentro do SQL, o acoplamento que o
-- contrato existe para eliminar.
--
-- As TRES colunas de valor nao sao arbitrarias: sao exatamente os tres tipos que a
-- interface selada ValorProjetado admite. A interface e fechada justamente porque
-- este esquema e contrato publicado — se ela pudesse ganhar tipos por acidente,
-- esta tabela ganharia colunas por acidente.
CREATE TABLE IF NOT EXISTS leitura (
    -- Quando o GATEWAY recebeu. Relogio real, mas carrega a latencia de transporte.
    instante_observado    TIMESTAMPTZ      NOT NULL,

    -- Quando a amostra foi tomada, DERIVADO da ancora de sessao de boot.
    --
    -- Coluna separada, e nunca sobrescrevendo a anterior. A origem so tem
    -- autoridade sobre o tempo monotonico; este valor e uma inferencia do gateway e
    -- carrega o erro da latencia no instante da ancoragem. Fundi-los faria uma
    -- estimativa passar por medicao.
    instante_estimado     TIMESTAMPTZ,

    -- O tempo monotonico BRUTO que a origem afirmou. Nunca derivado de nada.
    tempo_ligado_ms       BIGINT           NOT NULL,

    id_do_dispositivo     TEXT             NOT NULL,
    id_da_sessao_de_boot  TEXT             NOT NULL,
    numero_de_sequencia   BIGINT           NOT NULL,

    tipo_de_conteudo      TEXT             NOT NULL,
    classe_de_dado        TEXT             NOT NULL,

    nome_do_campo         TEXT             NOT NULL,
    valor_numerico        DOUBLE PRECISION,
    valor_texto           TEXT,
    valor_logico          BOOLEAN,

    -- A projecao e IDEMPOTENTE por esta restricao.
    --
    -- O cursor de projecao avanca DEPOIS de a gravacao estar confirmada aqui. Se o
    -- gateway cair entre as duas coisas, o intervalo e reprocessado — e precisa ser
    -- inofensivo. Sem esta restricao, cada queda duplicaria linhas e todo grafico
    -- de contagem ficaria errado.
    CONSTRAINT leitura_unica UNIQUE
        (id_do_dispositivo, id_da_sessao_de_boot, numero_de_sequencia, nome_do_campo, instante_observado)
);

-- Hypertable particionada por tempo: e o que da compressao de serie temporal e
-- agregacao continua. Sem ela, o volume dimensionado nao cabe num produto
-- replicado em varias plantas.
SELECT create_hypertable('leitura', by_range('instante_observado'), if_not_exists => TRUE);

-- Responde a consulta dominante do dashboard: um canal, uma janela de tempo.
CREATE INDEX IF NOT EXISTS idx_leitura_dispositivo_campo_tempo
    ON leitura (id_do_dispositivo, nome_do_campo, instante_observado DESC);

-- Responde as consultas por tipo de evento, que sao as do relatorio.
CREATE INDEX IF NOT EXISTS idx_leitura_tipo_tempo
    ON leitura (tipo_de_conteudo, instante_observado DESC);
