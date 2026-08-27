package com.synka.core.collector;

import com.synka.core.domain.settings.BufferSettings;
import com.synka.core.domain.settings.OpcUaSettings;
import com.synka.core.domain.settings.PostgresSettings;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.boot.autoconfigure.jdbc.DataSourceAutoConfiguration;
import org.springframework.boot.context.properties.EnableConfigurationProperties;

/**
 * Aplicação de coleta contínua. As configurações de banco (DataSources Postgres e
 * SQLite) são criadas explicitamente em {@code CollectorConfig}, por isso a
 * autoconfiguração de DataSource é desabilitada.
 */
@SpringBootApplication(exclude = DataSourceAutoConfiguration.class)
@EnableConfigurationProperties({PostgresSettings.class, OpcUaSettings.class, BufferSettings.class})
public class CollectorApplication {

    public static void main(String[] args) {
        SpringApplication.run(CollectorApplication.class, args);
    }
}
