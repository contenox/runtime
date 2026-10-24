package taskengine

import "strings"

type HandlerOutputMode int

const (
	HandlerOutputFixed HandlerOutputMode = iota
	HandlerOutputPassthrough
	HandlerOutputDynamic
	HandlerOutputNone
)

type HandlerSignature struct {
	Inputs       []DataType
	Mode         HandlerOutputMode
	Output       DataType
	SuccessEvals []string
}

func (s HandlerSignature) AcceptsInput(dt DataType) bool {
	if len(s.Inputs) == 0 {
		return true
	}
	for _, in := range s.Inputs {
		if in == dt {
			return true
		}
	}
	return false
}

func (s HandlerSignature) acceptsDescription() string {
	if len(s.Inputs) == 0 {
		return "any input type"
	}
	names := make([]string, len(s.Inputs))
	for i, in := range s.Inputs {
		d := in
		names[i] = d.String()
	}
	return strings.Join(names, ", ")
}

var handlerSignatures = map[TaskHandler]HandlerSignature{
	HandleChatCompletion: {
		Inputs:       []DataType{DataTypeString, DataTypeChatHistory},
		Mode:         HandlerOutputFixed,
		Output:       DataTypeChatHistory,
		SuccessEvals: []string{TransitionToolCall, TransitionExecuted},
	},
	HandleExecuteToolCalls: {
		Inputs:       []DataType{DataTypeChatHistory},
		Mode:         HandlerOutputFixed,
		Output:       DataTypeChatHistory,
		SuccessEvals: []string{TransitionNoop, TransitionNoCallsFound, TransitionToolsExecuted, TransitionFailed},
	},
	HandleRoute: {
		Inputs: []DataType{DataTypeString, DataTypeInt, DataTypeChatHistory},
		Mode:   HandlerOutputPassthrough,
	},
	HandleTools: {
		Mode: HandlerOutputDynamic,
	},
	HandleNoop: {
		Mode:         HandlerOutputPassthrough,
		SuccessEvals: []string{TransitionNoop},
	},
	HandleRaiseError: {
		Inputs: []DataType{DataTypeString, DataTypeInt, DataTypeChatHistory},
		Mode:   HandlerOutputNone,
	},
}

func HandlerSignatureFor(h TaskHandler) (HandlerSignature, bool) {
	sig, ok := handlerSignatures[h]
	return sig, ok
}
