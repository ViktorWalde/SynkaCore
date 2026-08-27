# GERADO POR ferramentas/geradordenopython — NAO EDITE.
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


def codificar_EnderecoDeCanal(campos):
    """Serializa EnderecoDeCanal. `campos` e um dict; chave ausente e campo omitido."""
    saida = bytearray()

    valor = campos.get("indice_do_modulo")
    if valor is not None:
        saida += _tag(1, 0) + _varint(valor)

    valor = campos.get("indice_do_canal")
    if valor is not None:
        saida += _tag(2, 0) + _varint(valor)

    return bytes(saida)


def codificar_AmostraEscalar(campos):
    """Serializa AmostraEscalar. `campos` e um dict; chave ausente e campo omitido."""
    saida = bytearray()

    valor = campos.get("endereco")
    if valor is not None:
        _bytes = codificar_EnderecoDeCanal(valor)
        saida += _tag(1, 2) + _varint(len(_bytes)) + _bytes

    valor = campos.get("valor")
    if valor is not None:
        saida += _tag(2, 5) + _float32(valor)

    return bytes(saida)


def codificar_SaudeDaOrigem(campos):
    """Serializa SaudeDaOrigem. `campos` e um dict; chave ausente e campo omitido."""
    saida = bytearray()

    valor = campos.get("bytes_livres_de_memoria")
    if valor is not None:
        saida += _tag(1, 0) + _varint(valor)

    valor = campos.get("maior_bloco_livre_em_bytes")
    if valor is not None:
        saida += _tag(2, 0) + _varint(valor)

    valor = campos.get("sinal_de_radio_dbm")
    if valor is not None:
        saida += _tag(3, 0) + _varint(valor)

    valor = campos.get("contagem_de_reinicios")
    if valor is not None:
        saida += _tag(4, 0) + _varint(valor)

    valor = campos.get("registros_descartados")
    if valor is not None:
        saida += _tag(5, 0) + _varint(valor)

    valor = campos.get("bytes_usados_no_buffer")
    if valor is not None:
        saida += _tag(6, 0) + _varint(valor)

    return bytes(saida)


def codificar_DescritorDeCanal(campos):
    """Serializa DescritorDeCanal. `campos` e um dict; chave ausente e campo omitido."""
    saida = bytearray()

    valor = campos.get("endereco")
    if valor is not None:
        _bytes = codificar_EnderecoDeCanal(valor)
        saida += _tag(1, 2) + _varint(len(_bytes)) + _bytes

    valor = campos.get("grandeza")
    if valor is not None:
        saida += _tag(2, 0) + _varint(valor)

    valor = campos.get("unidade")
    if valor is not None:
        _bytes = valor.encode('utf-8')
        saida += _tag(3, 2) + _varint(len(_bytes)) + _bytes

    valor = campos.get("periodo_de_amostragem_ms")
    if valor is not None:
        saida += _tag(4, 0) + _varint(valor)

    return bytes(saida)


def codificar_DescritorDaOrigem(campos):
    """Serializa DescritorDaOrigem. `campos` e um dict; chave ausente e campo omitido."""
    saida = bytearray()

    valor = campos.get("versao_do_firmware")
    if valor is not None:
        _bytes = valor.encode('utf-8')
        saida += _tag(1, 2) + _varint(len(_bytes)) + _bytes

    valor = campos.get("modelo_do_hardware")
    if valor is not None:
        _bytes = valor.encode('utf-8')
        saida += _tag(2, 2) + _varint(len(_bytes)) + _bytes

    valor = campos.get("canais")
    if valor is not None:
        for item in valor:
            _bytes = codificar_DescritorDeCanal(item)
            saida += _tag(3, 2) + _varint(len(_bytes)) + _bytes

    valor = campos.get("versao_do_catalogo_de_motivos")
    if valor is not None:
        saida += _tag(4, 0) + _varint(valor)

    return bytes(saida)


def codificar_Envelope(campos):
    """Serializa Envelope. `campos` e um dict; chave ausente e campo omitido."""
    saida = bytearray()

    valor = campos.get("numero_de_sequencia")
    if valor is not None:
        saida += _tag(1, 0) + _varint(valor)

    valor = campos.get("tempo_ligado_ms")
    if valor is not None:
        saida += _tag(2, 0) + _varint(valor)

    valor = campos.get("amostra_escalar")
    if valor is not None:
        _bytes = codificar_AmostraEscalar(valor)
        saida += _tag(3, 2) + _varint(len(_bytes)) + _bytes

    valor = campos.get("leitura_de_contador")
    if valor is not None:
        _bytes = codificar_LeituraDeContador(valor)
        saida += _tag(4, 2) + _varint(len(_bytes)) + _bytes

    valor = campos.get("transicao_digital")
    if valor is not None:
        _bytes = codificar_TransicaoDigital(valor)
        saida += _tag(5, 2) + _varint(len(_bytes)) + _bytes

    valor = campos.get("amostra_agregada")
    if valor is not None:
        _bytes = codificar_AmostraAgregada(valor)
        saida += _tag(6, 2) + _varint(len(_bytes)) + _bytes

    valor = campos.get("mudanca_de_estado_de_maquina")
    if valor is not None:
        _bytes = codificar_MudancaDeEstadoDeMaquina(valor)
        saida += _tag(7, 2) + _varint(len(_bytes)) + _bytes

    valor = campos.get("saude_da_origem")
    if valor is not None:
        _bytes = codificar_SaudeDaOrigem(valor)
        saida += _tag(8, 2) + _varint(len(_bytes)) + _bytes

    valor = campos.get("lacuna_de_buffer")
    if valor is not None:
        _bytes = codificar_LacunaDeBuffer(valor)
        saida += _tag(9, 2) + _varint(len(_bytes)) + _bytes

    valor = campos.get("descritor_da_origem")
    if valor is not None:
        _bytes = codificar_DescritorDaOrigem(valor)
        saida += _tag(10, 2) + _varint(len(_bytes)) + _bytes

    return bytes(saida)


def codificar_Remessa(campos):
    """Serializa Remessa. `campos` e um dict; chave ausente e campo omitido."""
    saida = bytearray()

    valor = campos.get("versao_do_esquema")
    if valor is not None:
        saida += _tag(1, 0) + _varint(valor)

    valor = campos.get("id_da_instalacao")
    if valor is not None:
        _bytes = valor.encode('utf-8')
        saida += _tag(2, 2) + _varint(len(_bytes)) + _bytes

    valor = campos.get("id_do_dispositivo")
    if valor is not None:
        _bytes = valor.encode('utf-8')
        saida += _tag(3, 2) + _varint(len(_bytes)) + _bytes

    valor = campos.get("id_da_sessao_de_boot")
    if valor is not None:
        _bytes = valor.encode('utf-8')
        saida += _tag(4, 2) + _varint(len(_bytes)) + _bytes

    valor = campos.get("envelopes")
    if valor is not None:
        for item in valor:
            _bytes = codificar_Envelope(item)
            saida += _tag(5, 2) + _varint(len(_bytes)) + _bytes

    return bytes(saida)


def decodificar_ConfirmacaoDeRemessa(dados):
    """Interpreta ConfirmacaoDeRemessa. Campos desconhecidos sao pulados."""
    resultado = {}
    posicao = 0
    while posicao < len(dados):
        chave, posicao = _ler_varint(dados, posicao)
        numero, tipo = chave >> 3, chave & 0x07
        valor, posicao = _ler_valor(dados, posicao, tipo)
        if numero == 1:
            resultado["duravel_ate_a_sequencia"] = valor
        elif numero == 2:
            _lista = resultado.setdefault("sequencias_rejeitadas_definitivamente", [])
            if tipo == 2:
                _lista.extend(_ler_varints(valor))
            else:
                _lista.append(valor)
        elif numero == 3:
            resultado["repetir_apos_ms"] = valor
    return resultado


# EstadoDeMaquina — gerado do contrato.
ESTADO_DE_MAQUINA_NAO_ESPECIFICADO = 0
ESTADO_DE_MAQUINA_RODANDO = 1
ESTADO_DE_MAQUINA_PARADA = 2
ESTADO_DE_MAQUINA_SETUP = 3
ESTADO_DE_MAQUINA_OCIOSA = 4
ESTADO_DE_MAQUINA_MANUTENCAO_PROGRAMADA = 5
ESTADO_DE_MAQUINA_QUEBRA = 6


# Grandeza — gerado do contrato.
GRANDEZA_NAO_ESPECIFICADO = 0
GRANDEZA_TEMPERATURA = 1
GRANDEZA_PRESSAO = 2
GRANDEZA_UMIDADE_DO_AR = 3
GRANDEZA_VAZAO = 4
GRANDEZA_MASSA = 5
GRANDEZA_NIVEL = 6
GRANDEZA_CORRENTE_ELETRICA = 7
GRANDEZA_ACELERACAO_DE_VIBRACAO = 8
GRANDEZA_ESTADO_DIGITAL = 9
GRANDEZA_CONTAGEM_DE_PECAS = 10
GRANDEZA_PH = 11
GRANDEZA_CONDUTIVIDADE_ELETRICA = 12
GRANDEZA_UMIDADE_DE_GRAO = 13
GRANDEZA_BRIX = 14
GRANDEZA_ROTACAO = 15
GRANDEZA_TORQUE = 16
GRANDEZA_VELOCIDADE_LINEAR = 17
GRANDEZA_TENSAO = 18
GRANDEZA_POTENCIA_ATIVA = 19
GRANDEZA_ENERGIA_ELETRICA = 20
