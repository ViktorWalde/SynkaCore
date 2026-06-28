package com.synka.core.domain;

/**
 * Falha ao persistir ou consultar leituras. Tipada para que o domínio não vaze
 * exceções específicas de tecnologia (JDBC, driver) e para que quem implementa um
 * {@code ReadingRepository} saiba o que sinalizar.
 */
public class PersistenceException extends Exception {

    private static final long serialVersionUID = 1L;

    public PersistenceException(String message) {
        super(message);
    }

    public PersistenceException(String message, Throwable cause) {
        super(message, cause);
    }
}
