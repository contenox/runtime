package acpsvc

import (
	"bytes"
	"encoding/json"
	"strconv"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// protocolNumbers rewrites each 64-bit integer of a declared message from the
// JSON string protojson writes into the JSON number the wire carries: proto3's
// JSON mapping quotes every 64-bit integer, and an entry's size and a run's
// offset reach a client as numbers, so a quoted one would be a different
// contract. A message that carries no 64-bit integer keeps protojson's bytes.
func protocolNumbers(msg proto.Message, encoded []byte) ([]byte, error) {
	desc := msg.ProtoReflect().Descriptor()
	if !carriesProtocolInteger(desc, map[protoreflect.FullName]bool{}) {
		return encoded, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var tree any
	if err := decoder.Decode(&tree); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(protocolIntegerNumbers(desc, tree)); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

// carriesProtocolInteger reports whether a declared message carries a 64-bit
// integer, itself or below one of its message fields.
func carriesProtocolInteger(desc protoreflect.MessageDescriptor, seen map[protoreflect.FullName]bool) bool {
	if seen[desc.FullName()] {
		return false
	}
	seen[desc.FullName()] = true
	fields := desc.Fields()
	for i := 0; i < fields.Len(); i++ {
		value := fields.Get(i)
		if value.IsMap() {
			value = value.MapValue()
		}
		if protocolIntegerKind(value.Kind()) {
			return true
		}
		if value.Kind() == protoreflect.MessageKind && carriesProtocolInteger(value.Message(), seen) {
			return true
		}
	}
	return false
}

// protocolIntegerKind reports whether a field kind is one of the 64-bit integers
// protojson writes as a JSON string.
func protocolIntegerKind(kind protoreflect.Kind) bool {
	switch kind {
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return true
	default:
		return false
	}
}

// protocolIntegerNumbers rewrites every quoted 64-bit integer below one decoded
// protojson rendering into the number it is.
func protocolIntegerNumbers(desc protoreflect.MessageDescriptor, value any) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	fields := desc.Fields()
	for name, member := range object {
		field := fields.ByJSONName(name)
		if field == nil {
			continue
		}
		switch {
		case field.IsMap():
			object[name] = protocolIntegerElements(field.MapValue().Kind(), field.MapValue().Message(), member)
		case field.IsList():
			object[name] = protocolIntegerElements(field.Kind(), field.Message(), member)
		case protocolIntegerKind(field.Kind()):
			object[name] = protocolIntegerNumber(member)
		case field.Kind() == protoreflect.MessageKind:
			object[name] = protocolIntegerNumbers(field.Message(), member)
		}
	}
	return object
}

// protocolIntegerElements rewrites the quoted 64-bit integers of a repeated or
// map rendering, whose elements are either the integers themselves or messages
// that carry one below them.
func protocolIntegerElements(kind protoreflect.Kind, element protoreflect.MessageDescriptor, value any) any {
	rewrite := func(item any) any {
		switch {
		case protocolIntegerKind(kind):
			return protocolIntegerNumber(item)
		case kind == protoreflect.MessageKind:
			return protocolIntegerNumbers(element, item)
		default:
			return item
		}
	}
	switch container := value.(type) {
	case []any:
		for i, item := range container {
			container[i] = rewrite(item)
		}
		return container
	case map[string]any:
		for name, item := range container {
			container[name] = rewrite(item)
		}
		return container
	default:
		return value
	}
}

// protocolIntegerNumber rewrites one quoted 64-bit integer into a JSON number; a
// value that is not one is returned unchanged.
func protocolIntegerNumber(value any) any {
	text, ok := value.(string)
	if !ok {
		return value
	}
	if signed, err := strconv.ParseInt(text, 10, 64); err == nil {
		return json.Number(strconv.FormatInt(signed, 10))
	}
	if unsigned, err := strconv.ParseUint(text, 10, 64); err == nil {
		return json.Number(strconv.FormatUint(unsigned, 10))
	}
	return value
}
