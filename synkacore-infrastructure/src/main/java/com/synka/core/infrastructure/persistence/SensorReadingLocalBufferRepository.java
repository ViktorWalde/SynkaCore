package com.synka.core.infrastructure.persistence;

import com.synka.core.domain.SensorReading;
import com.synka.core.domain.settings.BufferSettings;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.jdbc.core.simple.JdbcClient;
import org.sqlite.SQLiteDataSource;

import java.io.UncheckedIOException;
import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.time.format.DateTimeFormatter;
import java.util.List;

/**
 * Buffer local em SQLite para persistência de emergência durante indisponibilidade
 * do TimescaleDB. Garante zero perda de leituras em quedas de até horas, não apenas
 * segundos. Tabela append-only: leituras sincronizadas ficam com synced = 1 para
 * auditoria e diagnóstico forense de quedas passadas.
 */
public final class SensorReadingLocalBufferRepository {

    private static final Logger log = LoggerFactory.getLogger(SensorReadingLocalBufferRepository.class);
    private static final DateTimeFormatter ISO = DateTimeFormatter.ISO_OFFSET_DATE_TIME;

    private final JdbcClient jdbc;

    public SensorReadingLocalBufferRepository(BufferSettings settings) {
        String databasePath = settings.databasePath();
        ensureDirectory(databasePath);

        SQLiteDataSource dataSource = new SQLiteDataSource();
        dataSource.setUrl("jdbc:sqlite:" + databasePath);

        this.jdbc = JdbcClient.create(dataSource);
        initializeDatabase();
    }

    // Idempotente: seguro chamar múltiplas vezes sem efeito colateral.
    private void initializeDatabase() {
        jdbc.sql("""
                CREATE TABLE IF NOT EXISTS pending_readings (
                    id        INTEGER PRIMARY KEY AUTOINCREMENT,
                    timestamp TEXT    NOT NULL,
                    tag       TEXT    NOT NULL,
                    value     TEXT    NOT NULL,
                    unit      TEXT    NOT NULL,
                    synced    INTEGER NOT NULL DEFAULT 0
                )
                """).update();
    }

    public void savePending(SensorReading reading) {
        jdbc.sql("""
                INSERT INTO pending_readings (timestamp, tag, value, unit, synced)
                VALUES (?, ?, ?, ?, 0)
                """)
                .param(reading.timestamp().format(ISO))
                .param(reading.tag())
                .param(reading.value().toString())
                .param(reading.unit())
                .update();

        log.warn("Leitura salva no buffer local — Tag: {}, Timestamp: {}",
                reading.tag(), reading.timestamp());
    }

    public List<PendingReading> getPending(int limit) {
        return jdbc.sql("""
                        SELECT id, timestamp, tag, value, unit
                        FROM pending_readings
                        WHERE synced = 0
                        ORDER BY id ASC
                        LIMIT ?
                        """)
                .param(limit)
                .query((rs, rowNum) -> new PendingReading(
                        rs.getLong("id"),
                        rs.getString("timestamp"),
                        rs.getString("tag"),
                        rs.getString("value"),
                        rs.getString("unit")))
                .list();
    }

    public void markAsSynced(long id) {
        jdbc.sql("UPDATE pending_readings SET synced = 1 WHERE id = ?")
                .param(id)
                .update();
    }

    public int countPending() {
        return jdbc.sql("SELECT COUNT(*) FROM pending_readings WHERE synced = 0")
                .query(Integer.class)
                .single();
    }

    private static void ensureDirectory(String databasePath) {
        Path directory = Paths.get(databasePath).getParent();
        if (directory != null) {
            try {
                Files.createDirectories(directory);
            } catch (IOException e) {
                throw new UncheckedIOException(e);
            }
        }
    }
}
