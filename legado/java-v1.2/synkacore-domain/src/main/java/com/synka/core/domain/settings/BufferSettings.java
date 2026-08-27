package com.synka.core.domain.settings;

import jakarta.validation.constraints.Min;
import jakarta.validation.constraints.NotBlank;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.boot.context.properties.bind.DefaultValue;
import org.springframework.validation.annotation.Validated;

/**
 * Configuração do buffer local em SQLite. Validada no startup.
 */
@ConfigurationProperties("buffer")
@Validated
public record BufferSettings(

        // Caminho do arquivo SQLite. Aceita relativo ou absoluto. Em produção
        // recomenda-se caminho absoluto em disco com espaço garantido.
        @DefaultValue("buffer/synkacore_buffer.db")
        @NotBlank(message = "buffer.database-path é obrigatório.")
        String databasePath,

        // Quantas leituras sincronizar por ciclo quando o primário volta.
        // Reduzir em hardware limitado, aumentar em servidores com folga.
        @DefaultValue("10")
        @Min(value = 1, message = "buffer.sync-batch-size deve ser >= 1.")
        int syncBatchSize
) {
}
