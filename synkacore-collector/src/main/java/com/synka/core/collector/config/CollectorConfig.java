package com.synka.core.collector.config;

import com.synka.core.domain.contract.ProtocolReader;
import com.synka.core.domain.contract.ReadingRepository;
import com.synka.core.domain.contract.WorkerStateTracker;
import com.synka.core.domain.settings.BufferSettings;
import com.synka.core.domain.settings.OpcUaSettings;
import com.synka.core.domain.settings.PostgresSettings;
import com.synka.core.infrastructure.persistence.ResilientReadingRepository;
import com.synka.core.infrastructure.persistence.SensorReadingLocalBufferRepository;
import com.synka.core.infrastructure.persistence.SensorReadingRepository;
import com.synka.core.infrastructure.resilience.DatabaseResiliencePipelineBuilder;
import com.synka.core.infrastructure.resilience.ResiliencePipeline;
import com.synka.core.infrastructure.simulation.VacuumChamberSimulator;
import com.zaxxer.hikari.HikariDataSource;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.context.annotation.Primary;

import javax.sql.DataSource;

/**
 * Wiring explícito da cadeia de persistência resiliente. As camadas são registradas
 * em ordem: pipeline de resiliência → repositório primário (TimescaleDB) → buffer
 * local → decorator. O decorator é o que se expõe como {@link ReadingRepository}, de
 * modo que o Worker nunca conhece detalhes de fallback.
 */
@Configuration
public class CollectorConfig {

    @Bean(destroyMethod = "close")
    public DataSource postgresDataSource(PostgresSettings settings) {
        HikariDataSource dataSource = new HikariDataSource();
        dataSource.setJdbcUrl(settings.url());
        dataSource.setUsername(settings.username());
        dataSource.setPassword(settings.password());
        dataSource.setPoolName("synka-postgres");
        return dataSource;
    }

    @Bean
    public DatabaseResiliencePipelineBuilder resiliencePipelineBuilder(WorkerStateTracker stateTracker) {
        return new DatabaseResiliencePipelineBuilder(stateTracker);
    }

    @Bean
    public ResiliencePipeline resiliencePipeline(DatabaseResiliencePipelineBuilder builder) {
        return builder.build();
    }

    @Bean
    public SensorReadingRepository sensorReadingRepository(DataSource postgresDataSource,
                                                           ResiliencePipeline resiliencePipeline) {
        return new SensorReadingRepository(postgresDataSource, resiliencePipeline);
    }

    @Bean
    public SensorReadingLocalBufferRepository bufferRepository(BufferSettings bufferSettings) {
        return new SensorReadingLocalBufferRepository(bufferSettings);
    }

    // @Primary: é o decorator que o Worker recebe como ReadingRepository. O
    // repositório primário (SensorReadingRepository) é injetado aqui pelo tipo
    // concreto, nunca como ReadingRepository — evitando que o Worker o use direto
    // e bypasse todo o sistema de buffer.
    @Bean
    @Primary
    public ReadingRepository readingRepository(SensorReadingRepository primary,
                                               SensorReadingLocalBufferRepository buffer,
                                               BufferSettings bufferSettings) {
        return new ResilientReadingRepository(primary, buffer, bufferSettings);
    }

    // Nesta versão usa-se o VacuumChamberSimulator porque não há servidor OPC UA no
    // ambiente de desenvolvimento. Basta trocar este bean por um OpcUaProtocolReader
    // apontando para hardware real ou simulador — a interface mantém o Worker isolado.
    @Bean
    public ProtocolReader protocolReader(OpcUaSettings opcUaSettings) {
        return new VacuumChamberSimulator(opcUaSettings);
    }
}
