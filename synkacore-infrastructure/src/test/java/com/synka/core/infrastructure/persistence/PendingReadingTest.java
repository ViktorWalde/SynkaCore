package com.synka.core.infrastructure.persistence;

import com.synka.core.domain.SensorReading;
import org.junit.jupiter.api.Test;

import java.math.BigDecimal;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.time.format.DateTimeFormatter;
import java.time.format.DateTimeParseException;
import java.util.Locale;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

class PendingReadingTest {

    @Test
    void converteLinhaValidaEmSensorReading() {
        PendingReading row = new PendingReading(
                1L, "2026-06-28T03:57:44.933162502Z", "Esteira1.Temperatura", "72.50", "°C");

        SensorReading reading = row.toSensorReading();

        assertThat(reading.value()).isEqualByComparingTo("72.50");
        assertThat(reading.tag()).isEqualTo("Esteira1.Temperatura");
        assertThat(reading.unit()).isEqualTo("°C");
        assertThat(reading.timestamp())
                .isEqualTo(OffsetDateTime.parse("2026-06-28T03:57:44.933162502Z"));
    }

    @Test
    void roundTripPreservaValorETimestampExatos() {
        OffsetDateTime ts = OffsetDateTime.now(ZoneOffset.UTC);
        SensorReading original = new SensorReading(new BigDecimal("65.07"), "°C", "tagX", ts);

        // Espelha exatamente como o buffer grava em SensorReadingLocalBufferRepository.savePending.
        PendingReading row = new PendingReading(
                10L,
                original.timestamp().format(DateTimeFormatter.ISO_OFFSET_DATE_TIME),
                original.tag(),
                original.value().toString(),
                original.unit());

        SensorReading restored = row.toSensorReading();

        assertThat(restored.value()).isEqualByComparingTo(original.value());
        assertThat(restored.unit()).isEqualTo(original.unit());
        assertThat(restored.tag()).isEqualTo(original.tag());
        assertThat(restored.timestamp()).isEqualTo(original.timestamp());
    }

    @Test
    void valorMalFormadoLancaExcecao() {
        PendingReading row = new PendingReading(1L, "2026-06-28T00:00:00Z", "t", "não-é-número", "°C");
        assertThatThrownBy(row::toSensorReading).isInstanceOf(NumberFormatException.class);
    }

    @Test
    void timestampMalFormadoLancaExcecao() {
        PendingReading row = new PendingReading(1L, "ontem às 3h", "t", "10", "°C");
        assertThatThrownBy(row::toSensorReading).isInstanceOf(DateTimeParseException.class);
    }

    @Test
    void parsingDeValorEhIndependenteDeLocale() {
        Locale original = Locale.getDefault();
        try {
            // Locale com vírgula como separador decimal — não pode afetar o parsing.
            Locale.setDefault(Locale.GERMANY);
            PendingReading row = new PendingReading(1L, "2026-06-28T00:00:00Z", "t", "72.5", "°C");
            assertThat(row.toSensorReading().value()).isEqualByComparingTo("72.5");
        } finally {
            Locale.setDefault(original);
        }
    }
}
