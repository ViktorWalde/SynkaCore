package com.synka.core.domain.contract;

import com.synka.core.domain.ProtocolException;
import com.synka.core.domain.SensorReading;

/**
 * Contrato genérico para qualquer protocolo industrial. Cada implementação
 * concreta conhece seu próprio endpoint e protocolo. O Worker depende dessa
 * abstração, permitindo trocar OPC UA por Modbus ou simulação sem alterá-lo
 * (inversão de dependência).
 *
 * O cancelamento é cooperativo: durante o shutdown a thread do Worker é
 * interrompida, e as implementações devem respeitar {@link Thread#interrupt()}.
 */
public interface ProtocolReader {

    /** Estabelece conexão com o dispositivo industrial. */
    void connect() throws ProtocolException;

    /** Lê o valor atual de uma tag e retorna no formato unificado do Synka. */
    SensorReading read(String tag) throws ProtocolException;

    /** Encerra a conexão de forma limpa. */
    void disconnect() throws ProtocolException;
}
