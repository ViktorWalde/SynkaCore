package com.synka.core.infrastructure.resilience;

/**
 * Abstração de pipeline de resiliência. Envolve uma operação com circuit breaker,
 * retry e timeout, sem que o chamador conheça os detalhes da implementação.
 */
@FunctionalInterface
public interface ResiliencePipeline {
    void execute(ThrowingRunnable action) throws Exception;
}
