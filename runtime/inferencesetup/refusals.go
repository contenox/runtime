package inferencesetup

import "fmt"

// ModelNotDiscoveredError reports an assignment naming a model its connection
// does not serve. Curated is true when the model is a curated local artifact
// the connection can download, which a surface answers with the acquisition
// operation instead of a configuration repair.
type ModelNotDiscoveredError struct {
	Connection string
	Model      string
	Curated    bool
}

// Error implements error.
func (e *ModelNotDiscoveredError) Error() string {
	return fmt.Sprintf("model %q is not available on connection %q", e.Model, e.Connection)
}

// CompletionContractMissingError reports an autocomplete assignment whose model
// carries no declared code-completion contract.
type CompletionContractMissingError struct {
	Connection string
	Model      string
}

// Error implements error.
func (e *CompletionContractMissingError) Error() string {
	return fmt.Sprintf("model %q on connection %q has no code-completion contract", e.Model, e.Connection)
}

// ConnectionUnavailableError reports an assignment naming a connection that is
// absent or whose catalog could not be read, so no model of it can be checked.
type ConnectionUnavailableError struct {
	Connection string
	Reason     string
}

// Error implements error.
func (e *ConnectionUnavailableError) Error() string {
	return fmt.Sprintf("connection %q is unavailable: %s", e.Connection, e.Reason)
}
