package com.synka.core.domain;

/**
 * Falha ao comunicar com um dispositivo/protocolo industrial. Tipada para que o
 * domínio não vaze exceções específicas da stack de comunicação (OPC UA, Modbus)
 * e para que quem implementa um {@code ProtocolReader} saiba o que sinalizar.
 */
public class ProtocolException extends Exception {

    private static final long serialVersionUID = 1L;

    public ProtocolException(String message) {
        super(message);
    }

    public ProtocolException(String message, Throwable cause) {
        super(message, cause);
    }
}
