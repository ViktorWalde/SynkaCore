-- SynkaCore — inicialização do TimescaleDB
-- Executado automaticamente pelo container na primeira subida
-- (montado em /docker-entrypoint-initdb.d/).

-- Extensão de séries temporais
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Tabela principal de leituras de sensores
CREATE TABLE IF NOT EXISTS sensor_readings (
    time  TIMESTAMPTZ  NOT NULL,
    tag   TEXT         NOT NULL,
    value NUMERIC      NOT NULL,
    unit  TEXT         NOT NULL
);

-- Converte em hypertable particionada por tempo
SELECT create_hypertable('sensor_readings', by_range('time'), if_not_exists => TRUE);

-- Índice para consultas por tag em ordem cronológica decrescente
CREATE INDEX IF NOT EXISTS idx_sensor_readings_tag_time
    ON sensor_readings (tag, time DESC);
