package com.synka.core.infrastructure.protocol;

import com.synka.core.domain.ProtocolException;
import com.synka.core.domain.SensorReading;
import com.synka.core.domain.contract.ProtocolReader;
import com.synka.core.domain.settings.OpcUaSettings;
import org.eclipse.milo.opcua.sdk.client.OpcUaClient;
import org.eclipse.milo.opcua.stack.core.UaException;
import org.eclipse.milo.opcua.stack.core.types.builtin.DataValue;
import org.eclipse.milo.opcua.stack.core.types.builtin.DateTime;
import org.eclipse.milo.opcua.stack.core.types.builtin.NodeId;
import org.eclipse.milo.opcua.stack.core.types.enumerated.TimestampsToReturn;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.math.BigDecimal;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;

/**
 * Implementa {@link ProtocolReader} para o protocolo OPC UA, padrão moderno de
 * comunicação com CLPs em ambientes industriais. Encapsula a complexidade da stack
 * OPC UA, expondo uma interface simples ao resto do sistema. Seguir o contrato
 * permite trocar OPC UA por Modbus ou simulação sem alterar o Worker.
 *
 * Não é registrado no DI nesta versão (o {@code VacuumChamberSimulator} é usado no
 * lugar enquanto não há servidor OPC UA disponível). Será exercitado quando um
 * servidor OPC UA real ou simulado estiver disponível para teste.
 */
public class OpcUaProtocolReader implements ProtocolReader {

    private final OpcUaSettings settings;
    private final Logger log = LoggerFactory.getLogger(OpcUaProtocolReader.class);

    // A sessão é o estado mutável da classe. É nula antes de connect() e volta a ser
    // nula depois de disconnect(). Torná-lo explícito força quem usa a classe a pensar
    // sobre o ciclo de vida da conexão.
    private OpcUaClient client;

    public OpcUaProtocolReader(OpcUaSettings settings) {
        this.settings = settings;
    }

    @Override
    public void connect() throws ProtocolException {
        log.info("Conectando ao servidor OPC UA em {}", settings.endpointUrl());

        // create(endpointUrl) negocia o endpoint e usa configuração padrão de cliente
        // (identidade anônima, segurança None) — suficiente para desenvolvimento local.
        // Em produção, a política de certificados e a segurança SignAndEncrypt seriam
        // configuradas explicitamente.
        try {
            client = OpcUaClient.create(settings.endpointUrl());
            client.connect();
        } catch (UaException e) {
            throw new ProtocolException(
                    "Falha ao conectar ao servidor OPC UA em " + settings.endpointUrl(), e);
        }

        log.info("Conectado com sucesso ao servidor OPC UA");
    }

    @Override
    public SensorReading read(String tag) throws ProtocolException {
        if (client == null) {
            throw new IllegalStateException(
                    "Sessão OPC UA não está conectada. Chame connect() primeiro.");
        }

        // Lê o valor atual (AttributeId.Value implícito em readValue) do NodeId.
        DataValue dataValue;
        try {
            dataValue = client.readValue(
                    0.0,
                    TimestampsToReturn.Both,
                    NodeId.parse(settings.nodeId()));
        } catch (UaException e) {
            throw new ProtocolException("Falha ao ler NodeId " + settings.nodeId(), e);
        }

        // OPC UA distingue falha de comunicação de valor "ruim". Tratamos qualquer
        // status ruim como falha de leitura; o Worker decide se continua ou para.
        if (dataValue.getStatusCode().isBad()) {
            throw new ProtocolException(
                    "Falha ao ler NodeId " + settings.nodeId() + ": " + dataValue.getStatusCode());
        }

        // O valor vem como Object porque OPC UA suporta muitos tipos. A conversão via
        // String cobre os casos numéricos comuns; tipos não numéricos lançam exceção
        // capturada pelo Worker — comportamento correto.
        Object raw = dataValue.getValue().getValue();
        BigDecimal value = new BigDecimal(String.valueOf(raw));

        // SourceTime é quando o servidor capturou o valor — mais preciso que o relógio
        // local porque elimina latência de rede. Sem ele, usamos a hora atual.
        DateTime sourceTime = dataValue.getSourceTime();
        OffsetDateTime timestamp = (sourceTime != null && !sourceTime.isNull())
                ? sourceTime.getJavaInstant().atOffset(ZoneOffset.UTC)
                : OffsetDateTime.now(ZoneOffset.UTC);

        return new SensorReading(value, settings.unit(), settings.tagName(), timestamp);
    }

    @Override
    public void disconnect() throws ProtocolException {
        // Idempotência: chamar disconnect() múltiplas vezes não deve dar erro.
        if (client != null) {
            log.info("Desconectando do servidor OPC UA");
            try {
                client.disconnect();
            } catch (UaException e) {
                throw new ProtocolException("Falha ao desconectar do servidor OPC UA", e);
            } finally {
                client = null; // NOPMD: reset intencional do handle de sessão após desconectar
            }
        }
    }
}
