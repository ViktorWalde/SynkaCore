package com.synka.core.infrastructure.resilience;

import com.synka.core.domain.contract.WorkerStateTracker;
import io.github.resilience4j.circuitbreaker.CircuitBreaker;
import io.github.resilience4j.circuitbreaker.CircuitBreakerConfig;
import io.github.resilience4j.core.IntervalFunction;
import io.github.resilience4j.retry.Retry;
import io.github.resilience4j.retry.RetryConfig;
import io.github.resilience4j.timelimiter.TimeLimiter;
import io.github.resilience4j.timelimiter.TimeLimiterConfig;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.time.Duration;
import java.util.concurrent.Callable;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * Encapsula a construção da pipeline de resiliência com callbacks que atualizam
 * o estado do Worker. Permite que os callbacks usem injeção de dependência em vez
 * de logger estático ou referências globais.
 *
 * Ordem de execução (de fora para dentro), espelhando o comportamento clássico de
 * supervisão industrial: CircuitBreaker → Retry → Timeout. O CircuitBreaker
 * supervisiona o conjunto: se está aberto, a chamada falha imediatamente sem nem
 * chegar ao Retry, evitando martelar um banco inacessível.
 *
 * Os parâmetros de tuning são injetáveis ({@link Tuning}) para permitir que testes
 * usem tempos curtos sem alterar o comportamento de produção, que continua com os
 * valores default.
 */
public class DatabaseResiliencePipelineBuilder {

    private static final Logger log = LoggerFactory.getLogger(DatabaseResiliencePipelineBuilder.class);

    private final WorkerStateTracker stateTracker;
    private final Tuning tuning;

    public DatabaseResiliencePipelineBuilder(WorkerStateTracker stateTracker) {
        this(stateTracker, Tuning.defaults());
    }

    public DatabaseResiliencePipelineBuilder(WorkerStateTracker stateTracker, Tuning tuning) {
        this.stateTracker = stateTracker;
        this.tuning = tuning;
    }

    public ResiliencePipeline build() {
        CircuitBreaker circuitBreaker = createCircuitBreaker();
        Retry retry = createRetry();
        TimeLimiter timeLimiter = createTimeLimiter();

        // Executor dedicado para aplicar o timeout por tentativa. Vive pelo tempo de
        // vida da aplicação (a pipeline é singleton) e usa threads daemon, sendo
        // encerrado implicitamente no shutdown da JVM — não há ciclo de close por chamada.
        ExecutorService executor = Executors.newFixedThreadPool(2, runnable -> { // NOPMD: ver comentário acima
            Thread thread = new Thread(runnable, "synka-db-timeout");
            thread.setDaemon(true);
            return thread;
        });

        return action -> {
            // Timeout aplicado por tentativa (camada mais interna).
            Callable<Void> timed = TimeLimiter.decorateFutureSupplier(timeLimiter, () ->
                    executor.submit(() -> {
                        action.run();
                        return null;
                    }));

            // Retry (camada intermediária) e CircuitBreaker (camada externa).
            Callable<Void> retried = Retry.decorateCallable(retry, timed);
            Callable<Void> guarded = CircuitBreaker.decorateCallable(circuitBreaker, retried);

            guarded.call();
        };
    }

    private CircuitBreaker createCircuitBreaker() {
        // Taxa de falhas configurável em janela temporal, com mínimo de chamadas para
        // amostra estatística válida. Em ambiente industrial, metade das gravações
        // falhando já indica problema real — esperar 100% derrubaria o banco antes
        // de protegê-lo.
        CircuitBreakerConfig config = CircuitBreakerConfig.custom()
                .failureRateThreshold(tuning.failureRateThreshold())
                .slidingWindowType(CircuitBreakerConfig.SlidingWindowType.TIME_BASED)
                .slidingWindowSize(tuning.slidingWindowSeconds())
                .minimumNumberOfCalls(tuning.minimumCalls())
                .waitDurationInOpenState(tuning.breakDuration())
                .permittedNumberOfCallsInHalfOpenState(1)
                .build();

        CircuitBreaker circuitBreaker = CircuitBreaker.of("database", config);

        circuitBreaker.getEventPublisher().onStateTransition(event -> {
            switch (event.getStateTransition().getToState()) {
                case OPEN -> {
                    stateTracker.notifyDegraded();
                    log.error("Circuit breaker aberto — banco indisponível. Próxima tentativa em {}s",
                            tuning.breakDuration().toSeconds());
                }
                case CLOSED -> {
                    stateTracker.notifyConnected();
                    log.info("Circuit breaker fechado — banco recuperado");
                }
                case HALF_OPEN -> {
                    stateTracker.notifyRetrying();
                    log.info("Circuit breaker em teste — enviando chamada de verificação");
                }
                default -> {
                }
            }
        });

        return circuitBreaker;
    }

    private Retry createRetry() {
        // maxAttempts inclui a chamada inicial (default 4 = 1 inicial + 3 retentativas),
        // com backoff exponencial e jitter para evitar thundering herd entre instâncias.
        RetryConfig config = RetryConfig.custom()
                .maxAttempts(tuning.maxAttempts())
                .intervalFunction(IntervalFunction.ofExponentialRandomBackoff(
                        tuning.baseBackoff(), tuning.backoffMultiplier(), tuning.jitter()))
                .build();

        Retry retry = Retry.of("database", config);

        retry.getEventPublisher().onRetry(event -> {
            stateTracker.notifyRetrying();
            Throwable cause = event.getLastThrowable();
            log.warn("Falha na gravação — tentativa {}/{} em {}s. {}",
                    event.getNumberOfRetryAttempts(),
                    tuning.maxAttempts() - 1,
                    roundSeconds(event.getWaitInterval()),
                    cause != null ? cause.getMessage() : "");
        });

        return retry;
    }

    private TimeLimiter createTimeLimiter() {
        // Timeout por tentativa individual. Operações normais levam menos de 100ms;
        // o default (5s) é generoso para rede industrial lenta, mas rígido para não
        // travar o Worker.
        return TimeLimiter.of("database", TimeLimiterConfig.custom()
                .timeoutDuration(tuning.timeout())
                .cancelRunningFuture(true)
                .build());
    }

    private static double roundSeconds(Duration duration) {
        return Math.round(duration.toMillis() / 100.0) / 10.0;
    }

    /**
     * Parâmetros de tuning da pipeline. {@link #defaults()} reflete os valores de
     * produção; testes podem injetar tempos curtos.
     */
    public record Tuning(
            float failureRateThreshold,
            int slidingWindowSeconds,
            int minimumCalls,
            Duration breakDuration,
            int maxAttempts,
            Duration baseBackoff,
            double backoffMultiplier,
            double jitter,
            Duration timeout
    ) {
        public static Tuning defaults() {
            return new Tuning(
                    50f,
                    30,
                    10,
                    Duration.ofSeconds(30),
                    4,
                    Duration.ofSeconds(2),
                    2.0,
                    0.2,
                    Duration.ofSeconds(5));
        }
    }
}
