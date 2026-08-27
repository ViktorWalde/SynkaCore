package com.synka.core.infrastructure.persistence;

import com.synka.core.domain.SensorReading;
import com.synka.core.domain.settings.BufferSettings;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.math.BigDecimal;
import java.nio.file.Path;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.util.List;

import static org.assertj.core.api.Assertions.assertThat;

/**
 * Integração real contra um arquivo SQLite (sem mocks): exercita a DDL, o SQL e o
 * ciclo append-only do buffer de emergência.
 */
class SensorReadingLocalBufferRepositoryIT {

    @TempDir
    Path tempDir;

    private SensorReadingLocalBufferRepository buffer;

    private SensorReading reading(String value) {
        return new SensorReading(new BigDecimal(value), "°C", "Esteira1.Temperatura",
                OffsetDateTime.now(ZoneOffset.UTC));
    }

    @BeforeEach
    void setup() {
        String path = tempDir.resolve("buffer.db").toString();
        buffer = new SensorReadingLocalBufferRepository(new BufferSettings(path, 10));
    }

    @Test
    void persisteEContaPendentes() {
        assertThat(buffer.countPending()).isZero();

        buffer.savePending(reading("25.5"));

        assertThat(buffer.countPending()).isEqualTo(1);
    }

    @Test
    void getPendingRespeitaLimiteEOrdemCronologica() {
        for (int i = 0; i < 5; i++) {
            buffer.savePending(reading(i + ".0"));
        }

        List<PendingReading> page = buffer.getPending(3);

        assertThat(page).hasSize(3);
        assertThat(page.get(0).id()).isLessThan(page.get(1).id());
        assertThat(page.get(1).id()).isLessThan(page.get(2).id());
    }

    @Test
    void markAsSyncedRemoveDaContagemDePendentes() {
        buffer.savePending(reading("30.0"));
        long id = buffer.getPending(1).get(0).id();

        buffer.markAsSynced(id);

        assertThat(buffer.countPending()).isZero();
    }

    @Test
    void roundTripPreservaValorETimestamp() {
        SensorReading original = reading("72.5");
        buffer.savePending(original);

        SensorReading restored = buffer.getPending(1).get(0).toSensorReading();

        assertThat(restored.value()).isEqualByComparingTo(original.value());
        assertThat(restored.unit()).isEqualTo(original.unit());
        assertThat(restored.tag()).isEqualTo(original.tag());
        assertThat(restored.timestamp()).isEqualTo(original.timestamp());
    }
}
