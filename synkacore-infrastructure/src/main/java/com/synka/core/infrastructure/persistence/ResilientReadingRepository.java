package com.synka.core.infrastructure.persistence;

import com.synka.core.domain.PersistenceException;
import com.synka.core.domain.SensorReading;
import com.synka.core.domain.contract.ReadingRepository;
import com.synka.core.domain.settings.BufferSettings;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.List;

/**
 * Decorator que orquestra resiliência de persistência: grava no primário
 * (TimescaleDB) e, na indisponibilidade dele, redireciona para o buffer local.
 * Quando o primário volta, sincroniza o buffer oportunisticamente a cada gravação
 * bem-sucedida.
 *
 * O Worker não conhece esta classe: ele depende apenas de {@link ReadingRepository}.
 */
public class ResilientReadingRepository implements ReadingRepository {

    private static final Logger log = LoggerFactory.getLogger(ResilientReadingRepository.class);

    private final ReadingRepository primaryRepository;
    private final SensorReadingLocalBufferRepository buffer;
    private final int syncBatchSize;

    public ResilientReadingRepository(
            ReadingRepository primaryRepository,
            SensorReadingLocalBufferRepository buffer,
            BufferSettings bufferSettings) {
        this.primaryRepository = primaryRepository;
        this.buffer = buffer;
        this.syncBatchSize = bufferSettings.syncBatchSize();
    }

    @Override
    public void save(SensorReading reading) throws PersistenceException {
        try {
            primaryRepository.save(reading);
            syncBuffer();
        } catch (Exception primaryEx) {
            log.warn("TimescaleDB indisponível. Redirecionando para buffer local. Erro: {}",
                    primaryEx.getMessage());

            try {
                buffer.savePending(reading);
            } catch (Exception bufferEx) {
                log.error("CRÍTICO: TimescaleDB e buffer local falharam. "
                                + "Leitura perdida — Tag: {}, Timestamp: {}. "
                                + "Erro primário: {}. Erro buffer: {}",
                        reading.tag(), reading.timestamp(),
                        primaryEx.getMessage(), bufferEx.getMessage());

                PersistenceException failure = new PersistenceException(
                        "TimescaleDB e buffer local falharam — leitura perdida", bufferEx);
                // Preserva também a falha do primário (causa raiz original) para diagnóstico.
                failure.addSuppressed(primaryEx);
                throw failure;
            }
        }
    }

    @Override
    public void checkHealth() throws PersistenceException {
        primaryRepository.checkHealth();
    }

    private void syncBuffer() {
        int pendingCount = buffer.countPending();
        if (pendingCount == 0) {
            return;
        }

        List<PendingReading> pending = buffer.getPending(syncBatchSize);

        log.info("Sincronizando {} de {} leituras pendentes no buffer local",
                pending.size(), pendingCount);

        for (PendingReading pendingReading : pending) {
            SensorReading reading;

            try {
                reading = pendingReading.toSensorReading();
            } catch (Exception parseEx) {
                log.error("Leitura corrompida no buffer — Id: {}. Marcando como processada "
                        + "para não travar fila. Erro: {}", pendingReading.id(), parseEx.getMessage());
                buffer.markAsSynced(pendingReading.id());
                continue;
            }

            try {
                primaryRepository.save(reading);
                buffer.markAsSynced(pendingReading.id());
            } catch (Exception ex) {
                log.warn("Sincronização interrompida no item {}. Leituras restantes "
                        + "permanecem no buffer. Erro: {}", pendingReading.id(), ex.getMessage());
                break;
            }
        }
    }
}
