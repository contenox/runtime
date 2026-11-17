package acpsvc

import (
	"bytes"
	"encoding/json"

	protocolv1 "github.com/contenox/runtime/internal/protocol/v1"
	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/servicecontract"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// protocolJSON is the rendering of the declared wire contract: EmitUnpopulated
// keeps an empty scalar and an empty repeated field on the wire, where a
// consumer reads a repeated field as an array and a scalar as its empty value.
var protocolJSON = protojson.MarshalOptions{EmitUnpopulated: true}

// protocolRequest refuses any request field the declaration does not carry.
var protocolRequest = protojson.UnmarshalOptions{DiscardUnknown: false}

// marshalProtocol renders one declared message as the extension result.
func marshalProtocol(msg proto.Message) (json.RawMessage, *libacp.Error) {
	out, err := protocolJSON.Marshal(msg)
	if err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	numbers, err := protocolNumbers(msg, out)
	if err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	return numbers, nil
}

// marshalProtocolList renders the catalog as its declared bare JSON array.
func marshalProtocolList[T proto.Message](msgs []T) (json.RawMessage, *libacp.Error) {
	items := make([]json.RawMessage, 0, len(msgs))
	for _, msg := range msgs {
		raw, err := marshalProtocol(msg)
		if err != nil {
			return nil, err
		}
		items = append(items, raw)
	}
	out, err := json.Marshal(items)
	if err != nil {
		return nil, libacp.InternalError(err.Error())
	}
	return out, nil
}

// unmarshalProtocolRequest decodes params into the declared request message of
// area; absent params are an empty request and an undeclared field is refused.
func unmarshalProtocolRequest(area string, params json.RawMessage, msg proto.Message) *libacp.Error {
	if emptyProtocolParams(params) {
		return nil
	}
	if err := protocolRequest.Unmarshal(params, msg); err != nil {
		return libacp.InvalidParams(area + ": " + err.Error())
	}
	return nil
}

// emptyProtocolParams reports whether params carries no request at all: an
// absent member, an empty one, and the JSON null a JSON-RPC caller may send are
// the same empty request, while any other non-object stays a refusal.
func emptyProtocolParams(params json.RawMessage) bool {
	trimmed := bytes.TrimSpace(params)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// serviceContractWire maps a service's readable contract onto its declared
// message, so a describe operation renders with the same declared rendering as
// every other operation instead of the surface's own JSON encoder.
func serviceContractWire(c servicecontract.Contract) *protocolv1.ServiceContract {
	operations := make([]*protocolv1.ContractOperation, 0, len(c.Operations))
	for _, operation := range c.Operations {
		inputs := make([]*protocolv1.ContractInput, 0, len(operation.Inputs))
		for _, input := range operation.Inputs {
			inputs = append(inputs, &protocolv1.ContractInput{
				Id: input.ID, Summary: input.Summary, Type: input.Type,
				Required: input.Required, Values: input.Values,
			})
		}
		operations = append(operations, &protocolv1.ContractOperation{
			Id: operation.ID, Summary: operation.Summary,
			Description: operation.Description, Inputs: inputs,
		})
	}
	return &protocolv1.ServiceContract{
		Id: c.ID, Summary: c.Summary, Description: c.Description, Operations: operations,
	}
}
