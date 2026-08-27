package com.synka.core.infrastructure.simulation;

import com.synka.core.domain.SensorReading;
import com.synka.core.domain.settings.OpcUaSettings;
import org.junit.jupiter.api.Test;

import java.math.BigDecimal;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

class VacuumChamberSimulatorTest {

    private OpcUaSettings settings() {
        return new OpcUaSettings(
                "opc.tcp://localhost", "ns=1;i=1", "Esteira1.Temperatura", "°C", 2000, "SynkaCore");
    }

    @Test
    void readAntesDeConnectLancaErroClaro() {
        VacuumChamberSimulator sim = new VacuumChamberSimulator(settings());

        assertThatThrownBy(() -> sim.read("t"))
                .isInstanceOf(IllegalStateException.class)
                .hasMessageContaining("connect()");
    }

    @Test
    void aposConnectFaseOciosaFicaProximaDoAmbiente() {
        VacuumChamberSimulator sim = new VacuumChamberSimulator(settings());
        sim.connect();

        SensorReading reading = sim.read("t");

        // Logo após connect (elapsed ~0) → fase ociosa ~25°C com ruído de ±0.8°C.
        assertThat(reading.value()).isBetween(new BigDecimal("24.20"), new BigDecimal("25.80"));
        assertThat(reading.unit()).isEqualTo("°C");
        assertThat(reading.tag()).isEqualTo("Esteira1.Temperatura");
    }

    @Test
    void valorSempreComDuasCasasDecimais() {
        VacuumChamberSimulator sim = new VacuumChamberSimulator(settings());
        sim.connect();

        for (int i = 0; i < 50; i++) {
            assertThat(sim.read("t").value().scale()).isEqualTo(2);
        }
    }
}
