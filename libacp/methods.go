package libacp

import (
	"context"
	"encoding/json"
	"strings"
)

const ProtocolVersion = 1

const (
	MethodInitialize   = "initialize"
	MethodAuthenticate = "authenticate"
	MethodLogout       = "logout"

	MethodSessionNew             = "session/new"
	MethodSessionLoad            = "session/load"
	MethodSessionResume          = "session/resume"
	MethodSessionClose           = "session/close"
	MethodSessionDelete          = "session/delete"
	MethodSessionList            = "session/list"
	MethodSessionPrompt          = "session/prompt"
	MethodSessionCancel          = "session/cancel"
	MethodSessionUpdate          = "session/update"
	MethodSessionSetMode         = "session/set_mode"
	MethodSessionSetConfigOption = "session/set_config_option"
	MethodSessionSetModel        = "session/set_model"

	MethodSessionRequestPermission = "session/request_permission"

	MethodCancelRequest = "$/cancel_request"

	MethodFSReadTextFile  = "fs/read_text_file"
	MethodFSWriteTextFile = "fs/write_text_file"

	MethodTerminalCreate      = "terminal/create"
	MethodTerminalOutput      = "terminal/output"
	MethodTerminalWaitForExit = "terminal/wait_for_exit"
	MethodTerminalKill        = "terminal/kill"
	MethodTerminalRelease     = "terminal/release"

	MethodMCPMessage = "mcp/message"
)

const ExtensionMethodPrefix = "_"

func IsExtensionMethod(method string) bool {
	return strings.HasPrefix(method, ExtensionMethodPrefix)
}

type ExtRequestHandler func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, *Error)

type ExtNotificationHandler func(ctx context.Context, method string, params json.RawMessage)
