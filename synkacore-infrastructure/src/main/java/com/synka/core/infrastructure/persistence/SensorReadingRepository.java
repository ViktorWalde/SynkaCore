package com.synka.core.infrastructure.persistence;

import com.synka.core.domain.PersistenceException;
import com.synka.core.domain.SensorReading;
import com.synka.core.domain.contract.ReadingRepository;
import com.synka.core.infrastructure.resilience.ResiliencePipeline;
import org.springframework.dao.DataAccessException;
import org.springframework.jdbc.core.simple.JdbcClient;

import javax.sql.DataSource;

/**
 * Grava leituras no banco de séries temporais (TimescaleDB) sob a pipeline de
 * resiliência (circuit breaker + retry + timeout).
 */
public class SensorReadingRepository implements ReadingRepository {

    private final JdbcClient jdbc;
    private final ResiliencePipeline pipeline;

    public SensorReadingRepository(DataSource postgresDataSource, ResiliencePipeline pipeline) {
        this.jdbc = JdbcClient.create(postgresDataSource);
        this.pipeline = pipeline;
    }

    @Override
    public void save(SensorReading reading) throws PersistenceException {
        try {
            pipeline.execute(() ->
                    jdbc.sql("""
                            INSERT INTO sensor_readings (time, tag, value, unit)
                            VALUES (?, ?, ?, ?)
                            """)
                            .param(reading.timestamp())
                            .param(reading.tag())
                            .param(reading.value())
                            .param(reading.unit())
                            .update());
        } catch (Exception e) {
            throw new PersistenceException("Falha ao gravar leitura no TimescaleDB", e);
        }
    }

    @Override
    public void checkHealth() throws PersistenceException {
        try {
            jdbc.sql("SELECT 1").query(Integer.class).single();
        } catch (DataAccessException e) {
            throw new PersistenceException("Health check do TimescaleDB falhou", e);
        }
    }
}
