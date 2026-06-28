package com.synka.core.infrastructure.resilience;

import com.synka.core.domain.contract.WorkerStateTracker;
import org.junit.jupiter.api.Test;

import java.time.Duration;
import java.util.concurrent.atomic.AtomicInteger;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.Mockito.atLeast;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.verify;

class DatabaseResiliencePipelineBuilderTest {

    // Tuning com tempos curtos para testes rápidos e determinísticos (sem jitter).
    private DatabaseResiliencePipelineBuilder.Tuning fastTuning(int maxAttempts, Duration timeout) {
        return new DatabaseResiliencePipelineBuilder.Tuning(
                50f, 1, 10, Duration.ofMillis(50), maxAttempts,
                Duration.ofMillis(5), 2.0, 0.0, timeout);
    }

    private ResiliencePipeline pipeline(WorkerStateTracker tracker, int maxAttempts, Duration timeout) {
        return new DatabaseResiliencePipelineBuilder(tracker, fastTuning(maxAttempts, timeout)).build();
    }

    @Test
    void acaoBemSucedidaExecutaUmaVez() throws Exception {
        WorkerStateTracker tracker = mock(WorkerStateTracker.class);
        AtomicInteger calls = new AtomicInteger();

        pipeline(tracker, 4, Duration.ofSeconds(1)).execute(calls::incrementAndGet);

        assertThat(calls.get()).isEqualTo(1);
        verify(tracker, never()).notifyRetrying();
    }

    @Test
    void acaoQueFalhaSempreEsgotaTentativasERelanca() {
        WorkerStateTracker tracker = mock(WorkerStateTracker.class);
        AtomicInteger calls = new AtomicInteger();

        assertThatThrownBy(() -> pipeline(tracker, 3, Duration.ofSeconds(1)).execute(() -> {
            calls.incrementAndGet();
            throw new RuntimeException("falha persistente");
        })).isInstanceOf(Exception.class);

        assertThat(calls.get()).isEqualTo(3); // maxAttempts
        verify(tracker, atLeast(1)).notifyRetrying();
    }

    @Test
    void recuperaAposFalhasIntermitentes() throws Exception {
        WorkerStateTracker tracker = mock(WorkerStateTracker.class);
        AtomicInteger calls = new AtomicInteger();

        pipeline(tracker, 4, Duration.ofSeconds(1)).execute(() -> {
            if (calls.incrementAndGet() < 3) {
                throw new RuntimeException("intermitente");
            }
        });

        assertThat(calls.get()).isEqualTo(3);
    }

    @Test
    void acaoQueExcedeOTimeoutFalha() {
        WorkerStateTracker tracker = mock(WorkerStateTracker.class);

        assertThatThrownBy(() ->
                pipeline(tracker, 2, Duration.ofMillis(100)).execute(() -> Thread.sleep(2000)))
                .isInstanceOf(Exception.class);
    }
}
