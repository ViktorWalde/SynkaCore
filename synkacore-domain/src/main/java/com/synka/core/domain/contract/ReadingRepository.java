package com.synka.core.domain.contract;

import com.synka.core.domain.PersistenceException;
import com.synka.core.domain.SensorReading;

/**
 * Contrato de persistência de leituras. Qualquer implementação — TimescaleDB,
 * buffer local ou futura integração — deve respeitá-lo. O Worker depende dessa
 * abstração, não de tecnologia concreta.
 */
public interface ReadingRepository {

    void save(SensorReading reading) throws PersistenceException;

    void checkHealth() throws PersistenceException;
}
