package com.synka.core.api.config;

import com.synka.core.domain.settings.PostgresSettings;
import com.synka.core.infrastructure.persistence.SensorReadingQueryRepository;
import com.zaxxer.hikari.HikariDataSource;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

import javax.sql.DataSource;

@Configuration
public class ApiConfig {

    @Bean(destroyMethod = "close")
    public DataSource postgresDataSource(PostgresSettings settings) {
        HikariDataSource dataSource = new HikariDataSource();
        dataSource.setJdbcUrl(settings.url());
        dataSource.setUsername(settings.username());
        dataSource.setPassword(settings.password());
        dataSource.setPoolName("synka-postgres-api");
        return dataSource;
    }

    @Bean
    public SensorReadingQueryRepository sensorReadingQueryRepository(DataSource postgresDataSource) {
        return new SensorReadingQueryRepository(postgresDataSource);
    }
}
