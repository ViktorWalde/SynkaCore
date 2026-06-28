package com.synka.core.api;

import com.synka.core.infrastructure.persistence.SensorReadingQueryRepository;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.http.HttpStatus;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.util.LinkedHashMap;
import java.util.Map;

/**
 * Health check real: verifica conectividade com o banco antes de responder healthy.
 * Retornar healthy quando o banco está fora é ativamente enganoso para sistemas de
 * monitoramento e operadores de plantão.
 */
@RestController
public class HealthController {

    private static final Logger log = LoggerFactory.getLogger(HealthController.class);

    private final SensorReadingQueryRepository repository;

    public HealthController(SensorReadingQueryRepository repository) {
        this.repository = repository;
    }

    @GetMapping("/health")
    public ResponseEntity<Map<String, Object>> health() {
        try {
            repository.checkHealth();
            return ResponseEntity.ok(body("healthy", "connected"));
        } catch (Exception e) {
            log.warn("Health check falhou: {}", e.getMessage());
            return ResponseEntity.status(HttpStatus.SERVICE_UNAVAILABLE)
                    .body(body("degraded", "unavailable"));
        }
    }

    private static Map<String, Object> body(String status, String database) {
        Map<String, Object> body = new LinkedHashMap<>();
        body.put("status", status);
        body.put("database", database);
        body.put("timestamp", OffsetDateTime.now(ZoneOffset.UTC));
        return body;
    }
}
