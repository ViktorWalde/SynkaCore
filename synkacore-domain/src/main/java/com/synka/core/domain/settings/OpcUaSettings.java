package com.synka.core.domain.settings;

import jakarta.validation.constraints.Min;
import jakarta.validation.constraints.NotBlank;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.boot.context.properties.bind.DefaultValue;
import org.springframework.validation.annotation.Validated;

/**
 * Configuração tipada para leitura via OPC UA. Validada no startup.
 */
@ConfigurationProperties("opcua")
@Validated
public record OpcUaSettings(

        // Endpoint do servidor OPC UA - ex: opc.tcp://localhost:53530/OPCUA/SimulationServer
        @NotBlank(message = "opcua.endpoint-url é obrigatória.")
        String endpointUrl,

        // NodeId da tag a ser lida - ex: ns=3;i=1003
        @NotBlank(message = "opcua.node-id é obrigatório.")
        String nodeId,

        // Tag identificadora para o SensorReading - ex: Esteira1.Temperatura
        @NotBlank(message = "opcua.tag-name é obrigatório.")
        String tagName,

        // Unidade de medida do valor lido - ex: °C
        @DefaultValue("") String unit,

        // Intervalo entre leituras em milissegundos
        @DefaultValue("2000") @Min(value = 100, message = "opcua.interval-ms deve ser >= 100ms.")
        int intervalMs,

        // Nome da aplicação cliente exibido para o servidor OPC UA
        @DefaultValue("SynkaCore") String applicationName
) {
}
