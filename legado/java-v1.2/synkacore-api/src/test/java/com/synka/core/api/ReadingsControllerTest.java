package com.synka.core.api;

import com.synka.core.domain.SensorReading;
import com.synka.core.infrastructure.persistence.SensorReadingQueryRepository;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.WebMvcTest;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.web.servlet.MockMvc;

import java.math.BigDecimal;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.util.List;

import static org.mockito.Mockito.when;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

@WebMvcTest(ReadingsController.class)
class ReadingsControllerTest {

    @Autowired
    private MockMvc mvc;

    @MockitoBean
    private SensorReadingQueryRepository repository;

    @Test
    void retornaLeiturasEmJson() throws Exception {
        when(repository.getRecent()).thenReturn(List.of(
                new SensorReading(new BigDecimal("65.07"), "°C", "Esteira1.Temperatura",
                        OffsetDateTime.now(ZoneOffset.UTC))));

        mvc.perform(get("/readings"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$[0].tag").value("Esteira1.Temperatura"))
                .andExpect(jsonPath("$[0].unit").value("°C"))
                .andExpect(jsonPath("$[0].value").value(65.07));
    }

    @Test
    void retornaListaVaziaQuandoNaoHaLeituras() throws Exception {
        when(repository.getRecent()).thenReturn(List.of());

        mvc.perform(get("/readings"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$").isArray())
                .andExpect(jsonPath("$").isEmpty());
    }
}
