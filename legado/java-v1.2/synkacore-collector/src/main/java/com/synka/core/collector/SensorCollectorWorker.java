package com.synka.core.collector;

import com.synka.core.domain.SensorReading;
import com.synka.core.domain.contract.ProtocolReader;
import com.synka.core.domain.contract.ReadingRepository;
import com.synka.core.domain.contract.WorkerStateTracker;
import com.synka.core.domain.settings.OpcUaSettings;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.context.ConfigurableApplicationContext;
import org.springframework.context.SmartLifecycle;
import org.springframework.stereotype.Component;

/**
 * Worker de coleta contínua. Roda em thread própria gerenciada pelo ciclo de vida do
 * Spring ({@link SmartLifecycle}): conecta no start, coleta em loop respeitando o
 * intervalo configurado, e desconecta de forma graciosa no stop.
 *
 * O cancelamento é cooperativo via {@code volatile running} + interrupção da thread,
 * permitindo encerramento limpo durante o shutdown.
 */
@Component
public class SensorCollectorWorker implements SmartLifecycle {

    private static final Logger log = LoggerFactory.getLogger(SensorCollectorWorker.class);

    private final ReadingRepository repository;
    private final ProtocolReader protocolReader;
    private final WorkerStateTracker stateTracker;
    private final OpcUaSettings opcUaSettings;
    private final ConfigurableApplicationContext applicationContext;

    private volatile boolean running;
    private Thread workerThread;

    public SensorCollectorWorker(
            ReadingRepository repository,
            ProtocolReader protocolReader,
            WorkerStateTracker stateTracker,
            OpcUaSettings opcUaSettings,
            ConfigurableApplicationContext applicationContext) {
        this.repository = repository;
        this.protocolReader = protocolReader;
        this.stateTracker = stateTracker;
        this.opcUaSettings = opcUaSettings;
        this.applicationContext = applicationContext;
    }

    @Override
    public void start() {
        running = true;
        workerThread = new Thread(this::run, "synka-collector");
        workerThread.start();
    }

    @Override
    public void stop() {
        running = false;
        if (workerThread != null) {
            workerThread.interrupt();
            try {
                workerThread.join();
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
        }
    }

    @Override
    public boolean isRunning() {
        return running;
    }

    private void run() {
        log.info("SynkaCore Collector iniciado — Tag: {}, Intervalo: {}ms",
                opcUaSettings.tagName(), opcUaSettings.intervalMs());

        // Conexão inicial. Falha aqui é crítica — sem leitura não há coleta. Sistema
        // para com log explícito em vez de loop infinito.
        try {
            protocolReader.connect();
        } catch (Exception e) {
            log.error("Falha ao conectar ao protocolo de leitura no startup. Worker não pode operar.", e);
            stateTracker.notifyDegraded();
            running = false;
            applicationContext.close();
            return;
        }

        while (running && !Thread.currentThread().isInterrupted()) {
            try {
                SensorReading reading = protocolReader.read(opcUaSettings.tagName());
                repository.save(reading);

                log.info("Leitura processada — Tag: {}, Valor: {} {}",
                        reading.tag(), reading.value(), reading.unit());
            } catch (Exception e) {
                // Qualquer falha no ciclo (leitura, gravação ou erro inesperado) degrada
                // o Worker mas não o mata. O shutdown é tratado pela interrupção do
                // Thread.sleep abaixo e pela flag running.
                stateTracker.notifyDegraded();
                log.error("Worker DEGRADADO — falha no ciclo de coleta", e);
            }

            try {
                Thread.sleep(opcUaSettings.intervalMs());
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                break;
            }
        }

        // Desconexão graciosa durante shutdown.
        try {
            protocolReader.disconnect();
        } catch (Exception e) {
            log.warn("Falha ao desconectar do protocolo durante shutdown", e);
        }

        log.info("SynkaCore Collector encerrado");
    }
}
