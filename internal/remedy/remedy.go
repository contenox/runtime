// Package remedy names the controls that repair inference prerequisites.
package remedy

const (
	// PrepareLocal names the command that installs or updates the local inference worker.
	PrepareLocal = "run `/local prepare <llama|openvino>` to install or update the local worker"

	// PrepareLlama names the command that installs or updates the llama.cpp worker.
	PrepareLlama = "run `/local prepare llama` to install or update the llama.cpp worker"

	// UpdateLocal names the controls that replace a local worker and restart it.
	UpdateLocal = "update and restart local inference: run `/local prepare <llama|openvino>`, then restart the idle owner with the ACP worker restart operation"

	// ReplanPlacement names the controls that show the devices and CPU threads local inference can use and plan a role within them.
	ReplanPlacement = "`/local devices` lists the devices and CPU threads local inference can use, and `/role <role> auto` plans the role within them"

	// ReplanRole names the controls that verify a role again or plan it afresh for this host.
	ReplanRole = "re-run `/role <role> <connection> <model>` to verify the role again, or let `/role <role> auto` plan it for this host"

	// StopIdleWorker names the operation that stops an idle local worker to release its memory.
	StopIdleWorker = "run `contenox-runtime ps` and unload an idle model with `contenox-runtime stop <model>`"

	// FitRole names the controls that replace a role's model with one that fits this host.
	FitRole = "assign a model that fits with `/role <role> <connection> <model>`, or let `/role <role> auto` choose one"

	// AssignRole names the command that assigns a role and verifies it; running it again verifies the role again.
	AssignRole = "assign and verify the role with `/role <role> <connection> <model>`"

	// ReacquireModel names the command that verifies a curated model's bundle and downloads whatever is missing or changed.
	ReacquireModel = "reacquire the model bundle with `/local pull <model>`, which downloads whatever is missing or changed"

	// ChooseBackend names the operation that selects a role's native backend.
	ChooseBackend = "set the native backend for the role through `_contenox/workers/configure`"

	// SaveScheduling names the operation that saves the shared owner's scheduling policy.
	SaveScheduling = "save the shared scheduling policy through `_contenox/workers/admission`"

	// AutocompleteRole names the command that assigns the Autocomplete role and verifies it.
	AutocompleteRole = "`/role autocomplete <connection> <model>` assigns the Autocomplete role and verifies it"

	// VerifyAutocomplete names the command that verifies the Autocomplete role again with a real completion.
	VerifyAutocomplete = "verify Autocomplete with `/role autocomplete <connection> <model>`"

	// VerifyNextEdit names the command that verifies the Next Edit role again with a real prediction.
	VerifyNextEdit = "verify Next Edit with `/role next-edit <connection> <model>`"

	// NextEditRole names the command that assigns the Next Edit role and verifies it.
	NextEditRole = "`/role next-edit <connection> <model>` assigns the Next Edit role and verifies it"

	// EmbeddingRole names the command that assigns the embedding model workspace search uses and verifies it.
	EmbeddingRole = "`/role embedding <connection> <model>` assigns an embedding model and verifies it"

	// DeclareCompletion names the command that declares how a model completes code.
	DeclareCompletion = "`/completion <connection> <model> instruct|prompt|fim <format>` declares how the model completes code"

	// PickConnection names the commands that list connections and assign a role to one of them.
	PickConnection = "`/providers` lists the connections; name the one to use in `/role <role> <connection> <model>`"

	// RepairConnection names the commands that show a connection's state and sign a subscription provider in.
	RepairConnection = "`/providers` shows why the connection is unavailable, and `contenox-runtime backend login <connection>` signs a subscription provider in"

	// ConnectionState names the command that shows a connection's current state.
	ConnectionState = "`/providers` shows the connection's current state"

	// RemoveConnection names the commands that list connections and delete one that is not in use.
	RemoveConnection = "`/providers` lists the connections and `contenox-runtime backend remove <connection>` deletes one you do not use"

	// ReconnectEndpoint names the commands that replace a connection saved without an endpoint.
	ReconnectEndpoint = "remove the connection with `contenox-runtime backend remove <connection>` and connect it again with `contenox-runtime backend add <name> --type <type> --url <endpoint>`"
)

// RaiseCeiling names the command that raises the share of pool that local inference may use.
func RaiseCeiling(pool string) string {
	return "raise the ceiling for " + pool + " with `/local ceiling <connection> " + pool + " <percent>`"
}

// PullAndAssign names the commands that download model and assign it to role on connection.
func PullAndAssign(model, role, connection string) string {
	return "download it with `/local pull " + model + "` and assign it with `/role " + role + " " + connection + " " + model + "`"
}
