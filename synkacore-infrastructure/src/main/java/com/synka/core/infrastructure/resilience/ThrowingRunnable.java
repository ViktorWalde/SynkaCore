package com.synka.core.infrastructure.resilience;

/** Ação que pode lançar exceção verificada, executada sob a pipeline de resiliência. */
@FunctionalInterface
public interface ThrowingRunnable {
    void run() throws Exception;
}
