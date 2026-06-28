package com.synka.core.infrastructure.simulation;

import com.synka.core.domain.SensorReading;
import com.synka.core.domain.contract.ProtocolReader;
import com.synka.core.domain.settings.OpcUaSettings;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.math.BigDecimal;
import java.math.RoundingMode;
import java.time.Instant;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.util.concurrent.ThreadLocalRandom;

/**
 * Simula leituras de uma câmara de vácuo em processo industrial real (curtimento de
 * couro, secagem, conservação). Implementa {@link ProtocolReader} para que o Worker
 * não saiba se está lendo de hardware ou simulação.
 *
 * <p>Ciclo do processo modelado (180 segundos no total):
 * <pre>
 *   0-30s    : ocioso (~25°C, ambiente)
 *   30-60s   : aquecimento progressivo (25°C → 65°C)
 *   60-120s  : manutenção em temperatura de processo (~65°C com variação realista)
 *   120-150s : resfriamento progressivo (65°C → 25°C)
 *   150-180s : ocioso novamente
 * </pre>
 *
 * Será substituído por {@code OpcUaProtocolReader} quando hardware real ou servidor
 * OPC UA simulado estiver disponível para teste.
 */
public class VacuumChamberSimulator implements ProtocolReader {

    private static final Logger log = LoggerFactory.getLogger(VacuumChamberSimulator.class);

    // Parâmetros físicos do processo. Temperaturas realistas para câmara de vácuo de
    // curtimento baseadas em literatura técnica.
    private static final double AMBIENT_TEMPERATURE = 25.0;
    private static final double PROCESS_TEMPERATURE = 65.0;
    private static final double NOISE_AMPLITUDE_C = 0.8;

    // Fases do ciclo em segundos.
    private static final int IDLE_PHASE_END_SECONDS = 30;
    private static final int RAMP_UP_END_SECONDS = 60;
    private static final int HOLD_PHASE_END_SECONDS = 120;
    private static final int RAMP_DOWN_END_SECONDS = 150;
    private static final int CYCLE_DURATION_SECONDS = 180;

    private final OpcUaSettings settings;

    // Início do ciclo. Inicializado em connect() para que a simulação comece do zero a
    // cada conexão, espelhando o comportamento de equipamento real.
    private volatile Instant cycleStart;

    public VacuumChamberSimulator(OpcUaSettings settings) {
        this.settings = settings;
    }

    @Override
    public void connect() {
        cycleStart = Instant.now();
        log.info("Simulador VacuumChamber conectado — Tag: {}, Duração do ciclo: {}s",
                settings.tagName(), CYCLE_DURATION_SECONDS);
    }

    @Override
    public SensorReading read(String tag) {
        if (cycleStart == null) {
            throw new IllegalStateException(
                    "Simulador não conectado. Chame connect() antes de read().");
        }

        double elapsedSeconds = (Instant.now().toEpochMilli() - cycleStart.toEpochMilli()) / 1000.0;
        double cyclePosition = elapsedSeconds % CYCLE_DURATION_SECONDS;

        double baseTemperature = calculatePhaseTemperature(cyclePosition);
        double noise = (ThreadLocalRandom.current().nextDouble() - 0.5) * 2 * NOISE_AMPLITUDE_C;
        BigDecimal value = BigDecimal.valueOf(baseTemperature + noise).setScale(2, RoundingMode.HALF_UP);

        return new SensorReading(
                value,
                settings.unit(),
                settings.tagName(),
                OffsetDateTime.now(ZoneOffset.UTC));
    }

    @Override
    public void disconnect() {
        log.info("Simulador VacuumChamber desconectado");
    }

    // Interpolação linear entre fases. Reflete o comportamento térmico real de
    // aquecimento e resfriamento progressivo em equipamento industrial.
    private static double calculatePhaseTemperature(double cyclePosition) {
        if (cyclePosition < IDLE_PHASE_END_SECONDS) {
            return AMBIENT_TEMPERATURE;
        }

        if (cyclePosition < RAMP_UP_END_SECONDS) {
            double phaseProgress =
                    (cyclePosition - IDLE_PHASE_END_SECONDS) / (RAMP_UP_END_SECONDS - IDLE_PHASE_END_SECONDS);
            return AMBIENT_TEMPERATURE + (PROCESS_TEMPERATURE - AMBIENT_TEMPERATURE) * phaseProgress;
        }

        if (cyclePosition < HOLD_PHASE_END_SECONDS) {
            return PROCESS_TEMPERATURE;
        }

        if (cyclePosition < RAMP_DOWN_END_SECONDS) {
            double phaseProgress =
                    (cyclePosition - HOLD_PHASE_END_SECONDS) / (RAMP_DOWN_END_SECONDS - HOLD_PHASE_END_SECONDS);
            return PROCESS_TEMPERATURE - (PROCESS_TEMPERATURE - AMBIENT_TEMPERATURE) * phaseProgress;
        }

        return AMBIENT_TEMPERATURE;
    }
}
