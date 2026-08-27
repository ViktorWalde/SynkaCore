package com.synka.core.api;

import com.synka.core.infrastructure.persistence.SensorReadingQueryRepository;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.WebMvcTest;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.web.servlet.MockMvc;

import static org.mockito.Mockito.doThrow;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

@WebMvcTest(HealthController.class)
class HealthControllerTest {

    @Autowired
    private MockMvc mvc;

    @MockitoBean
    private SensorReadingQueryRepository repository;

    @Test
    void healthyQuandoBancoResponde() throws Exception {
        // checkHealth() não lança → banco disponível.
        mvc.perform(get("/health"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.status").value("healthy"))
                .andExpect(jsonPath("$.database").value("connected"));
    }

    @Test
    void degradadoQuandoBancoForaRetorna503() throws Exception {
        doThrow(new RuntimeException("db down")).when(repository).checkHealth();

        mvc.perform(get("/health"))
                .andExpect(status().isServiceUnavailable())
                .andExpect(jsonPath("$.status").value("degraded"))
                .andExpect(jsonPath("$.database").value("unavailable"));
    }
}
