package com.synka.core.infrastructure.persistence;

import com.synka.core.domain.SensorReading;

import java.math.BigDecimal;
import java.time.OffsetDateTime;

/**
 * Linha pendente no buffer local. Existe apenas para mapear as colunas do SQLite.
 * value e timestamp são TEXT: {@code new BigDecimal(String)} e ISO-8601 são
 * independentes de locale, preservando precisão exata em qualquer máquina.
 */
public record PendingReading(
        long id,
        String timestamp,
        String tag,
        String value,
        String unit
) {
    public SensorReading toSensorReading() {
        return new SensorReading(
                new BigDecimal(value),
                unit,
                tag,
                OffsetDateTime.parse(timestamp));
    }
}
