-- Acrescenta ao modelo de leitura o que a configuracao da instalacao deriva.
--
-- Antes desta migracao, a tabela guardava "dispositivo camara-01, canal 0,
-- value=24,7". Isso e verdade e nao responde nada: nao se sabe se e temperatura ou
-- pressao, em que unidade, nem de qual equipamento. Um dashboard construido sobre
-- isso mostraria indices de canal para o operador.
--
-- Todas as colunas sao ANULAVEIS, e isso e deliberado. Canal que chega sem
-- configuracao continua sendo gravado — com as colunas nulas — em vez de recusado.
-- Recusar significaria perder dado durante o comissionamento, que e exatamente
-- quando canal nao configurado acontece. NULO aqui significa "o gateway ainda nao
-- sabe o que isto mede", que e informacao honesta.

-- O ponto de medicao a que este canal estava vinculado.
--
-- A serie historica pertence ao PONTO, nao ao dispositivo: trocar um sensor
-- queimado nao pode romper a continuidade. Por isso as consultas de tendencia
-- devem agrupar por esta coluna, e nao por id_do_dispositivo.
ALTER TABLE leitura ADD COLUMN IF NOT EXISTS id_do_ponto_de_medicao TEXT;

-- O que a instalacao declara que este canal mede, e em que unidade.
--
-- Desnormalizado na propria linha, e nao numa tabela de dimensao com junção.
-- Duas razoes: a consulta dominante e uma serie temporal filtrada por ponto, e
-- junção em hypertable de bilhoes de linhas custa caro; e a configuracao MUDA ao
-- longo do tempo — se a unidade de um ponto for corrigida amanha, a linha gravada
-- hoje precisa continuar dizendo em que unidade ela foi de fato registrada.
ALTER TABLE leitura ADD COLUMN IF NOT EXISTS grandeza TEXT;
ALTER TABLE leitura ADD COLUMN IF NOT EXISTS unidade  TEXT;

-- Marca leitura fora da faixa plausivel do instrumento.
--
-- MARCA, nunca recusa: a origem mediu aquilo, e descartar apagaria justamente o
-- sintoma de um transdutor descalibrado ou de um cabo rompido. O valor fica na
-- serie com a anomalia declarada ao lado, e quem consulta decide o que fazer.
--
-- NULO significa "sem faixa configurada", que e diferente de FALSE ("dentro da
-- faixa"). Colapsar os dois faria uma instalacao ainda nao configurada parecer
-- inteiramente saudavel.
ALTER TABLE leitura ADD COLUMN IF NOT EXISTS fora_de_faixa BOOLEAN;

-- O rotulo do motivo de parada, resolvido pelo catalogo da instalacao.
--
-- A resolucao acontece no gateway e o codigo bruto permanece na coluna
-- valor_numerico do campo reason_code: o derivado nunca sobrescreve o afirmado.
-- Se o catalogo for corrigido, o rotulo e recomputavel a partir do diario.
ALTER TABLE leitura ADD COLUMN IF NOT EXISTS rotulo_do_motivo TEXT;

-- A consulta dominante do dashboard passa a ser por PONTO, nao por dispositivo.
CREATE INDEX IF NOT EXISTS idx_leitura_ponto_campo_tempo
    ON leitura (id_do_ponto_de_medicao, nome_do_campo, instante_observado DESC)
    WHERE id_do_ponto_de_medicao IS NOT NULL;

-- Responde "o que esta chegando sem configuracao?" sem varrer a tabela inteira.
-- Indice parcial: em operacao saudavel ele fica praticamente vazio e custa nada.
CREATE INDEX IF NOT EXISTS idx_leitura_sem_ponto
    ON leitura (id_do_dispositivo, instante_observado DESC)
    WHERE id_do_ponto_de_medicao IS NULL;
