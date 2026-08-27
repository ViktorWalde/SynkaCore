package com.synka.core.infrastructure.persistence;

import com.synka.core.domain.SensorReading;
import com.synka.core.domain.WorkerState;
import com.synka.core.domain.contract.WorkerStateTracker;
import com.synka.core.infrastructure.resilience.DatabaseResiliencePipelineBuilder;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.jdbc.core.simple.JdbcClient;
import org.springframework.jdbc.datasource.DriverManagerDataSource;
import org.testcontainers.containers.PostgreSQLContainer;
import org.testcontainers.junit.jupiter.Container;
import org.testcontainers.junit.jupiter.Testcontainers;
import org.testcontainers.utility.DockerImageName;

import javax.sql.DataSource;
import java.math.BigDecimal;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.util.List;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatCode;

/**
 * Integração real contra um TimescaleDB em container (sem mocks): exercita a
 * gravação resiliente e a consulta, validando o SQL e o mapeamento de tipos contra
 * um PostgreSQL de verdade. Pulado automaticamente em ambientes sem Docker.
 */
@Testcontainers(disabledWithoutDocker = true)
class SensorReadingRepositoryIT {

    @Container
    static final PostgreSQLContainer<?> DATABASE = new PostgreSQLContainer<>(
            DockerImageName.parse("timescale/timescaledb:latest-pg17")
                    .asCompatibleSubstituteFor("postgres"))
            .withDatabaseName("synkacore")
            .withUsername("postgres")
            .withPassword("synkacore");

    private SensorReadingRepository writeRepository;
    private SensorReadingQueryRepository queryRepository;

    @BeforeEach
    void setup() {
        DataSource dataSource = dataSource();

        JdbcClient jdbc = JdbcClient.create(dataSource);
        jdbc.sql("""
                CREATE TABLE IF NOT EXISTS sensor_readings (
                    time  TIMESTAMPTZ NOT NULL,
                    tag   TEXT        NOT NULL,
                    value NUMERIC     NOT NULL,
                    unit  TEXT        NOT NULL
                )
                """).update();
        jdbc.sql("DELETE FROM sensor_readings").update();

        writeRepository = new SensorReadingRepository(dataSource,
                new DatabaseResiliencePipelineBuilder(new NoopStateTracker()).build());
        queryRepository = new SensorReadingQueryRepository(dataSource);
    }

    private DataSource dataSource() {
        DriverManagerDataSource dataSource = new DriverManagerDataSource(
                DATABASE.getJdbcUrl(), DATABASE.getUsername(), DATABASE.getPassword());
        dataSource.setDriverClassName("org.postgresql.Driver");
        return dataSource;
    }

    @Test
    void gravaEConsultaRoundTrip() throws Exception {
        SensorReading reading = new SensorReading(
                new BigDecimal("65.07"), "°C", "Esteira1.Temperatura",
                OffsetDateTime.now(ZoneOffset.UTC));

        writeRepository.save(reading);
        List<SensorReading> recent = queryRepository.getRecent();

        assertThat(recent).hasSize(1);
        assertThat(recent.get(0).value()).isEqualByComparingTo("65.07");
        assertThat(recent.get(0).tag()).isEqualTo("Esteira1.Temperatura");
        assertThat(recent.get(0).unit()).isEqualTo("°C");
    }

    @Test
    void getRecentLimitaEm100EOrdenaPorTempoDescendente() throws Exception {
        OffsetDateTime base = OffsetDateTime.now(ZoneOffset.UTC).minusMinutes(200);
        for (int i = 0; i < 120; i++) {
            writeRepository.save(new SensorReading(
                    new BigDecimal(i + ".0"), "°C", "tag", base.plusMinutes(i)));
        }

        List<SensorReading> recent = queryRepository.getRecent();

        assertThat(recent).hasSize(100);
        // Ordem descendente: a primeira leitura é a mais recente.
        assertThat(recent.get(0).timestamp()).isAfter(recent.get(99).timestamp());
    }

    @Test
    void checkHealthPassaComBancoDisponivel() {
        assertThatCode(() -> queryRepository.checkHealth()).doesNotThrowAnyException();
    }

    /** Tracker sem efeito — o teste foca na persistência, não no estado do Worker. */
    private static final class NoopStateTracker implements WorkerStateTracker {
        @Override
        public void notifyRetrying() {
        }

        @Override
        public void notifyDegraded() {
        }

        @Override
        public void notifyConnected() {
        }

        @Override
        public WorkerState currentState() {
            return WorkerState.CONECTADO;
        }
    }
}
