package com.synka.core.domain.settings;

import jakarta.validation.constraints.NotBlank;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.boot.context.properties.bind.DefaultValue;
import org.springframework.validation.annotation.Validated;

/**
 * Configuração tipada do banco de séries temporais (TimescaleDB).
 * Validada no startup — falha rápida com mensagem clara é preferível a falha
 * silenciosa durante operação em ambiente industrial.
 */
@ConfigurationProperties("postgres")
@Validated
public record PostgresSettings(

        @NotBlank(message = "postgres.url é obrigatória. Verifique o application.yml ou variáveis de ambiente.")
        String url,

        @DefaultValue("") String username,

        @DefaultValue("") String password
) {
}
