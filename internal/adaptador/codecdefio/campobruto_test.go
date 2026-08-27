package codecdefio_test

import (
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// protoreflectRawVarint monta os bytes de um campo varint com o numero indicado.
//
// Existe para que o teste possa simular um campo que NENHUMA versao atual do
// contrato declara — que e exatamente o que uma origem com firmware mais novo
// enviaria. Sem isso, a propriedade de compatibilidade para frente so poderia ser
// verificada editando o .proto, o que faria o teste depender de um estado do
// contrato que ele mesmo teria de desfazer.
func protoreflectRawVarint(numeroDoCampo protowire.Number, valor uint64) protoreflect.RawFields {
	bytes := protowire.AppendTag(nil, numeroDoCampo, protowire.VarintType)
	return protowire.AppendVarint(bytes, valor)
}
