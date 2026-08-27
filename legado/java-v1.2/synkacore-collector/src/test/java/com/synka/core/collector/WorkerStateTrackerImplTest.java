package com.synka.core.collector;

import com.synka.core.domain.WorkerState;
import org.junit.jupiter.api.Test;

import static org.assertj.core.api.Assertions.assertThat;

class WorkerStateTrackerImplTest {

    @Test
    void estadoInicialEhConectado() {
        assertThat(new WorkerStateTrackerImpl().currentState()).isEqualTo(WorkerState.CONECTADO);
    }

    @Test
    void transicoesRefletemAsNotificacoes() {
        WorkerStateTrackerImpl tracker = new WorkerStateTrackerImpl();

        tracker.notifyRetrying();
        assertThat(tracker.currentState()).isEqualTo(WorkerState.RECONECTANDO);

        tracker.notifyDegraded();
        assertThat(tracker.currentState()).isEqualTo(WorkerState.DEGRADADO);

        tracker.notifyConnected();
        assertThat(tracker.currentState()).isEqualTo(WorkerState.CONECTADO);
    }

    @Test
    void notificacaoRepetidaMantemEstado() {
        WorkerStateTrackerImpl tracker = new WorkerStateTrackerImpl();

        tracker.notifyDegraded();
        tracker.notifyDegraded();

        assertThat(tracker.currentState()).isEqualTo(WorkerState.DEGRADADO);
    }

    @Test
    void leituraConcorrenteRefleteUltimaTransicao() throws InterruptedException {
        WorkerStateTrackerImpl tracker = new WorkerStateTrackerImpl();

        Thread writer = new Thread(tracker::notifyDegraded);
        writer.start();
        writer.join();

        // Campo volatile garante visibilidade cross-thread da última transição.
        assertThat(tracker.currentState()).isEqualTo(WorkerState.DEGRADADO);
    }
}
