package com.synka.core.domain.contract;

import com.synka.core.domain.WorkerState;

/**
 * Abstração para que os callbacks da pipeline de resiliência atualizem o estado
 * do Worker sem depender do tipo concreto do Collector.
 */
public interface WorkerStateTracker {

    void notifyRetrying();

    void notifyDegraded();

    void notifyConnected();

    WorkerState currentState();
}
