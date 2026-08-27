package com.synka.core.infrastructure.protocol;

import com.synka.core.domain.SensorReading;
import com.synka.core.domain.contract.ProtocolReader;

/**
 * Stub para implementação futura de Modbus TCP. Mantido implementando o contrato
 * para garantir verificação no compilador e impedir uso acidental antes da
 * implementação real. Será implementado quando hardware Modbus estiver disponível.
 */
public class ModbusProtocolReader implements ProtocolReader {

    @Override
    public void connect() {
        throw new UnsupportedOperationException(
                "ModbusProtocolReader ainda não implementado. "
                        + "Será implementado quando hardware Modbus estiver disponível.");
    }

    @Override
    public SensorReading read(String tag) {
        throw new UnsupportedOperationException("ModbusProtocolReader ainda não implementado.");
    }

    @Override
    public void disconnect() {
        throw new UnsupportedOperationException("ModbusProtocolReader ainda não implementado.");
    }
}
