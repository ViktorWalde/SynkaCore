package main

// preambulo e o cabecalho fixo do modulo gerado: as primitivas do formato de fio.
//
// Escrito a mao aqui, e nao derivado do descritor, porque ele nao depende do
// contrato — sao as regras do protobuf, que nao mudam. O que muda com o contrato
// sao os codificadores por mensagem, e esses sim sao gerados.
const preambulo = `# GERADO POR ferramentas/geradordenopython — NAO EDITE.
#
# Codificador protobuf do no SynkaCore, para MicroPython.
#
# Regerar:  make no-micropython
#
# Este arquivo existe porque o contrato .proto e a FONTE UNICA do sistema, e nao ha
# gerador protobuf para MicroPython que sirva: o uprotobuf so fala proto2 (sem oneof
# nem optional, que este contrato usa) e o minipb exige esquema escrito a mao — que
# divergiria do contrato sem nada acusar.
#
# O modulo cobre apenas o que o no precisa: codificar o que ele ENVIA e decodificar
# a confirmacao. Nao e um protobuf de proposito geral e nao deve virar um.
#
# Python puro, sem dependencia. Roda em MicroPython e em CPython — e roda em CPython
# de proposito, porque e assim que o teste de fidelidade compara os bytes que este
# arquivo produz com os que o gateway em Go produz para a mesma mensagem.

import struct


def _varint(valor):
    """Codifica um inteiro nao negativo no formato varint base-128."""
    if valor < 0:
        raise ValueError("varint negativo nao ocorre neste contrato")
    saida = bytearray()
    while True:
        septeto = valor & 0x7F
        valor >>= 7
        if valor:
            saida.append(septeto | 0x80)
        else:
            saida.append(septeto)
            return bytes(saida)


def _tag(numero, tipo):
    """Codifica a chave de um campo: numero do campo mais tipo de fio."""
    return _varint((numero << 3) | tipo)


def _float32(valor):
    """Codifica um float em 4 bytes little-endian.

    float32 e nao float64 porque e o que o contrato declara: conversores
    analogico-digitais industriais tem 12 a 16 bits, e float32 carrega ~7 digitos
    significativos. double dobraria os bytes no fio sem acrescentar informacao.
    """
    return struct.pack("<f", valor)


def _ler_varint(dados, posicao):
    """Le um varint a partir de posicao. Devolve (valor, nova_posicao)."""
    valor = 0
    deslocamento = 0
    while True:
        if posicao >= len(dados):
            raise ValueError("varint truncado")
        octeto = dados[posicao]
        posicao += 1
        valor |= (octeto & 0x7F) << deslocamento
        if not octeto & 0x80:
            return valor, posicao
        deslocamento += 7
        if deslocamento > 63:
            raise ValueError("varint longo demais")


def _ler_varints(dados):
    """Desempacota uma sequencia de varints concatenados.

    Escalar repetido em proto3 viaja EMPACOTADO: todos os valores num unico campo
    length-delimited, em vez de um por tag. Sem desempacotar, a lista chegaria ao no
    como um bloco de bytes sem significado.
    """
    valores = []
    posicao = 0
    while posicao < len(dados):
        valor, posicao = _ler_varint(dados, posicao)
        valores.append(valor)
    return valores


def _ler_valor(dados, posicao, tipo):
    """Le um valor pelo tipo de fio. Devolve (valor, nova_posicao).

    Tipos desconhecidos levantam excecao em vez de serem adivinhados: um quadro que
    nao se sabe interpretar nao deve produzir um numero plausivel.
    """
    if tipo == 0:
        return _ler_varint(dados, posicao)
    if tipo == 2:
        tamanho, posicao = _ler_varint(dados, posicao)
        return dados[posicao:posicao + tamanho], posicao + tamanho
    if tipo == 5:
        return struct.unpack("<f", dados[posicao:posicao + 4])[0], posicao + 4
    if tipo == 1:
        return struct.unpack("<d", dados[posicao:posicao + 8])[0], posicao + 8
    raise ValueError("tipo de fio nao suportado: %d" % tipo)
`
