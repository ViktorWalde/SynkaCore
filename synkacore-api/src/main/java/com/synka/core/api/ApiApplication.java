package com.synka.core.api;

import com.synka.core.domain.settings.PostgresSettings;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.boot.autoconfigure.jdbc.DataSourceAutoConfiguration;
import org.springframework.boot.context.properties.EnableConfigurationProperties;

/**
 * Aplicação REST de consulta de leituras e health check. O DataSource Postgres é
 * criado explicitamente em {@code ApiConfig}, por isso a autoconfiguração de
 * DataSource é desabilitada.
 */
@SpringBootApplication(exclude = DataSourceAutoConfiguration.class)
@EnableConfigurationProperties(PostgresSettings.class)
public class ApiApplication {

    public static void main(String[] args) {
        SpringApplication.run(ApiApplication.class, args);
    }
}
