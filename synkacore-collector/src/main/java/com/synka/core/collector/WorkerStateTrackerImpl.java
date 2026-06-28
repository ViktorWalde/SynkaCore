package com.synka.core.collector;

import com.synka.core.domain.WorkerState;
import com.synka.core.domain.contract.WorkerStateTracker;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.stereotype.Component;

/**
 * Implementação compartilhada do estado do Worker. Singleton para que os callbacks
 * da pipeline de resiliência e o Worker observem a mesma instância de estado.
 *
 * Campo {@code volatile} garante leitura cross-thread atômica sem cache local. Para
 * atualização única (escrita simples, não read-modify-write), volatile é suficiente e
 * mais leve que um lock.
 */
@Component
public class WorkerStateTrackerImpl implements WorkerStateTracker {

    private static final Logger log = LoggerFactory.getLogger(WorkerStateTrackerImpl.class);

    private volatile WorkerState state = WorkerState.CONECTADO;

    @Override
    public WorkerState currentState() {
        return state;
    }

    @Override
    public void notifyRetrying() {
        transitionTo(WorkerState.RECONECTANDO);
    }

    @Override
    public void notifyDegraded() {
        transitionTo(WorkerState.DEGRADADO);
    }

    @Override
    public void notifyConnected() {
        transitionTo(WorkerState.CONECTADO);
    }

    private synchronized void transitionTo(WorkerState newState) {
        WorkerState current = state;
        if (current == newState) {
            return;
        }

        state = newState;
        log.info("Worker estado: {} -> {}", current, newState);
    }
}
