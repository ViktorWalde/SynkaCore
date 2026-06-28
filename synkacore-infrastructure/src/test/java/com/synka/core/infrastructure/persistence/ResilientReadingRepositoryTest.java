package com.synka.core.infrastructure.persistence;

import com.synka.core.domain.PersistenceException;
import com.synka.core.domain.SensorReading;
import com.synka.core.domain.contract.ReadingRepository;
import com.synka.core.domain.settings.BufferSettings;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.math.BigDecimal;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.util.List;

import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyInt;
import static org.mockito.ArgumentMatchers.anyLong;
import static org.mockito.Mockito.doNothing;
import static org.mockito.Mockito.doThrow;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.times;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

class ResilientReadingRepositoryTest {

    private ReadingRepository primary;
    private SensorReadingLocalBufferRepository buffer;
    private ResilientReadingRepository repository;

    private SensorReading reading(String value) {
        return new SensorReading(new BigDecimal(value), "°C", "tag", OffsetDateTime.now(ZoneOffset.UTC));
    }

    private PendingReading pending(long id, String value) {
        return new PendingReading(id, "2026-06-28T00:00:0" + id + "Z", "tag", value, "°C");
    }

    @BeforeEach
    void setup() {
        primary = mock(ReadingRepository.class);
        buffer = mock(SensorReadingLocalBufferRepository.class);
        repository = new ResilientReadingRepository(primary, buffer, new BufferSettings("buffer/x.db", 10));
    }

    @Test
    void happyPathNaoTocaOBuffer() throws Exception {
        when(buffer.countPending()).thenReturn(0);

        repository.save(reading("25.0"));

        verify(primary).save(any());
        verify(buffer, never()).savePending(any());
    }

    @Test
    void primarioIndisponivelCaiNoBuffer() throws Exception {
        doThrow(new RuntimeException("db down")).when(primary).save(any());
        SensorReading r = reading("25.0");

        repository.save(r);

        verify(buffer).savePending(r);
    }

    @Test
    void primarioEBufferFalhamRelancaExcecao() throws Exception {
        doThrow(new RuntimeException("db down")).when(primary).save(any());
        doThrow(new RuntimeException("disk full")).when(buffer).savePending(any());

        // Falha dupla é re-lançada como PersistenceException tipada, preservando a
        // causa original (a falha do buffer) para diagnóstico.
        assertThatThrownBy(() -> repository.save(reading("25.0")))
                .isInstanceOf(PersistenceException.class)
                .cause().hasMessage("disk full");
    }

    @Test
    void sincronizaBufferQuandoPrimarioVolta() throws Exception {
        when(buffer.countPending()).thenReturn(2);
        when(buffer.getPending(anyInt())).thenReturn(List.of(pending(1L, "10.0"), pending(2L, "11.0")));

        repository.save(reading("25.0"));

        // 1 leitura original + 2 pendências drenadas.
        verify(primary, times(3)).save(any());
        verify(buffer).markAsSynced(1L);
        verify(buffer).markAsSynced(2L);
    }

    @Test
    void leituraCorrompidaNoBufferEMarcadaComoSyncedSemTravarFila() throws Exception {
        when(buffer.countPending()).thenReturn(2);
        when(buffer.getPending(anyInt())).thenReturn(List.of(
                new PendingReading(1L, "data-inválida", "tag", "abc", "°C"), // corrompida
                pending(2L, "11.0")));                                       // válida

        repository.save(reading("25.0"));

        // Corrompida é marcada (não trava a fila); a válida é gravada e marcada.
        verify(buffer).markAsSynced(1L);
        verify(buffer).markAsSynced(2L);
        // primary.save: original + a válida (a corrompida nunca chega ao primário).
        verify(primary, times(2)).save(any());
    }

    @Test
    void sincronizacaoParaNoPrimeiroErroSemMarcarRestante() throws Exception {
        when(buffer.countPending()).thenReturn(2);
        when(buffer.getPending(anyInt())).thenReturn(List.of(pending(1L, "10.0"), pending(2L, "11.0")));
        // Leitura original grava ok; a primeira pendência falha → break.
        doNothing().doThrow(new RuntimeException("db down again")).when(primary).save(any());

        repository.save(reading("25.0"));

        verify(buffer, never()).markAsSynced(anyLong());
    }
}
