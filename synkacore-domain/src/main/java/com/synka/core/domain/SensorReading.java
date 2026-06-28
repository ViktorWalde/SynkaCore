package com.synka.core.domain;

import java.math.BigDecimal;
import java.time.OffsetDateTime;

/**
 * Leitura de sensor já normalizada para o formato unificado do Synka.
 * Tipo imutável (record) que trafega entre todas as camadas.
 */
public record SensorReading(
        BigDecimal value,
        String unit,
        String tag,
        OffsetDateTime timestamp
) {
}
