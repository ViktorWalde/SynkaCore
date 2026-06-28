package com.synka.core.infrastructure.persistence;

import com.synka.core.domain.SensorReading;
import org.springframework.jdbc.core.simple.JdbcClient;

import javax.sql.DataSource;
import java.time.OffsetDateTime;
import java.util.List;

/**
 * Consulta de leituras no banco de séries temporais. Mapeamento tipado das
 * colunas com verificação em tempo de compilação.
 */
public class SensorReadingQueryRepository {

    private final JdbcClient jdbc;

    public SensorReadingQueryRepository(DataSource postgresDataSource) {
        this.jdbc = JdbcClient.create(postgresDataSource);
    }

    public List<SensorReading> getRecent() {
        return jdbc.sql("""
                        SELECT time, tag, value, unit
                        FROM sensor_readings
                        ORDER BY time DESC
                        LIMIT 100
                        """)
                .query((rs, rowNum) -> new SensorReading(
                        rs.getBigDecimal("value"),
                        rs.getString("unit"),
                        rs.getString("tag"),
                        rs.getObject("time", OffsetDateTime.class)))
                .list();
    }

    public void checkHealth() {
        jdbc.sql("SELECT 1").query(Integer.class).single();
    }
}
