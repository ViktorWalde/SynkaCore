package com.synka.core.api;

import com.synka.core.domain.SensorReading;
import com.synka.core.infrastructure.persistence.SensorReadingQueryRepository;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

import java.util.List;

@RestController
public class ReadingsController {

    private final SensorReadingQueryRepository repository;

    public ReadingsController(SensorReadingQueryRepository repository) {
        this.repository = repository;
    }

    @GetMapping("/readings")
    public List<SensorReading> getReadings() {
        return repository.getRecent();
    }
}
