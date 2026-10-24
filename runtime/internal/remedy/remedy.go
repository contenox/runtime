package remedy

const (
	PrepareLocal = "run `/local prepare <llama|openvino>` to install or update the local worker"

	PrepareLlama = "run `/local prepare llama` to install or update the llama.cpp worker"

	UpdateLocal = "update and restart local inference: run `/local prepare <llama|openvino>`, then Contenox: Restart Idle Local Inference"

	ReplanPlacement = "`/local devices` lists the devices and CPU threads local inference can use, and `/role <role> auto` plans the role within them"

	ReplanRole = "re-run `/role <role> <connection> <model>` to verify the role again, or let `/role <role> auto` plan it for this host"

	StopIdleWorker = "stop an idle worker with Contenox: Manage Model Workers"

	FitRole = "assign a model that fits with `/role <role> <connection> <model>`, or let `/role <role> auto` choose one"

	AssignRole = "assign and verify the role with `/role <role> <connection> <model>`"

	ReacquireModel = "reacquire the model bundle with `/local pull <model>`, which downloads whatever is missing or changed"

	ChooseBackend = "choose another native backend for the role with Contenox: Configure Local Model Workers"

	SaveScheduling = "save the scheduling policy again with Contenox: Configure Model Scheduling"

	AutocompleteRole = "`/role autocomplete <connection> <model>` assigns the Autocomplete role and verifies it"

	VerifyAutocomplete = "verify Autocomplete with `/role autocomplete <connection> <model>`"

	VerifyNextEdit = "verify Next Edit with `/role next-edit <connection> <model>`"

	NextEditRole = "`/role next-edit <connection> <model>` assigns the Next Edit role and verifies it"

	EmbeddingRole = "`/role embedding <connection> <model>` assigns an embedding model and verifies it"

	DeclareCompletion = "`/completion <connection> <model> instruct|prompt|fim <format>` declares how the model completes code"

	PickConnection = "`/providers` lists the connections; name the one to use in `/role <role> <connection> <model>`"

	RepairConnection = "`/providers` shows why the connection is unavailable, and `/provider login <connection>` signs a subscription provider in"

	ConnectionState = "`/providers` shows the connection's current state"

	RemoveConnection = "`/providers` lists the connections and `/provider remove <connection>` deletes one you do not use"

	ReconnectEndpoint = "remove the connection with `/provider remove <connection>` and connect it again with `/provider connect <type> [endpoint]`"
)

func RaiseCeiling(pool string) string {
	return "raise the ceiling for " + pool + " with `/local ceiling <connection> " + pool + " <percent>`"
}

func PullAndAssign(model, role, connection string) string {
	return "download it with `/local pull " + model + "` and assign it with `/role " + role + " " + connection + " " + model + "`"
}
