package libacp

import "context"

type Client interface {
	RequestPermission(ctx context.Context, req RequestPermissionRequest) (RequestPermissionResponse, error)
	ReadTextFile(ctx context.Context, req ReadTextFileRequest) (ReadTextFileResponse, error)
	WriteTextFile(ctx context.Context, req WriteTextFileRequest) (WriteTextFileResponse, error)
	CreateTerminal(ctx context.Context, req CreateTerminalRequest) (CreateTerminalResponse, error)
	TerminalOutput(ctx context.Context, req TerminalOutputRequest) (TerminalOutputResponse, error)
	WaitForTerminalExit(ctx context.Context, req WaitForTerminalExitRequest) (WaitForTerminalExitResponse, error)
	KillTerminal(ctx context.Context, req KillTerminalRequest) (KillTerminalResponse, error)
	ReleaseTerminal(ctx context.Context, req ReleaseTerminalRequest) (ReleaseTerminalResponse, error)
	MessageMCP(ctx context.Context, req MessageMCPRequest) (MessageMCPResponse, error)
	SessionUpdate(ctx context.Context, n SessionNotification) error
}

type ClientFactory func(conn *ClientSideConnection) Client

type UnimplementedClient struct{}

func (UnimplementedClient) RequestPermission(context.Context, RequestPermissionRequest) (RequestPermissionResponse, error) {
	return RequestPermissionResponse{}, MethodNotFound(MethodSessionRequestPermission)
}
func (UnimplementedClient) ReadTextFile(context.Context, ReadTextFileRequest) (ReadTextFileResponse, error) {
	return ReadTextFileResponse{}, MethodNotFound(MethodFSReadTextFile)
}
func (UnimplementedClient) WriteTextFile(context.Context, WriteTextFileRequest) (WriteTextFileResponse, error) {
	return WriteTextFileResponse{}, MethodNotFound(MethodFSWriteTextFile)
}
func (UnimplementedClient) CreateTerminal(context.Context, CreateTerminalRequest) (CreateTerminalResponse, error) {
	return CreateTerminalResponse{}, MethodNotFound(MethodTerminalCreate)
}
func (UnimplementedClient) TerminalOutput(context.Context, TerminalOutputRequest) (TerminalOutputResponse, error) {
	return TerminalOutputResponse{}, MethodNotFound(MethodTerminalOutput)
}
func (UnimplementedClient) WaitForTerminalExit(context.Context, WaitForTerminalExitRequest) (WaitForTerminalExitResponse, error) {
	return WaitForTerminalExitResponse{}, MethodNotFound(MethodTerminalWaitForExit)
}
func (UnimplementedClient) KillTerminal(context.Context, KillTerminalRequest) (KillTerminalResponse, error) {
	return KillTerminalResponse{}, MethodNotFound(MethodTerminalKill)
}
func (UnimplementedClient) ReleaseTerminal(context.Context, ReleaseTerminalRequest) (ReleaseTerminalResponse, error) {
	return ReleaseTerminalResponse{}, MethodNotFound(MethodTerminalRelease)
}
func (UnimplementedClient) MessageMCP(context.Context, MessageMCPRequest) (MessageMCPResponse, error) {
	return MessageMCPResponse{}, MethodNotFound(MethodMCPMessage)
}
func (UnimplementedClient) SessionUpdate(context.Context, SessionNotification) error {
	return nil
}
