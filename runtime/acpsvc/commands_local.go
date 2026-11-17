package acpsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	protocolv1 "github.com/contenox/runtime/internal/protocol/v1"
	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/runtime/autosetup"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

func localUsage() string {
	return "usage: /local prepare <llama|openvino> | /local models | /local pull <model> | /local devices | /local ceiling <connection> <pool> <percent>"
}

func (t *inferenceHandler) handleLocal(ctx context.Context, fields []string) (string, error) {
	if len(fields) == 0 {
		return "", errors.New(localUsage())
	}
	switch fields[0] {
	case "prepare":
		return t.handleLocalPrepare(ctx, fields[1:])
	case "models":
		if len(fields) == 1 {
			return t.handleLocalModels(ctx)
		}
	case "pull":
		return t.handleLocalPull(ctx, fields[1:])
	case "devices":
		if len(fields) == 1 {
			return t.handleLocalDevices(ctx)
		}
	case "ceiling":
		return t.handleLocalCeiling(ctx, fields[1:])
	}
	return "", errors.New(localUsage())
}

func (t *inferenceHandler) handleLocalPrepare(ctx context.Context, fields []string) (string, error) {
	if len(fields) != 1 {
		return "", errors.New("usage: /local prepare <llama|openvino>")
	}
	result, err := t.runInference(ctx, &protocolv1.InferenceRequest{
		Action: "prepare-native", NativePrerequisite: &protocolv1.InferenceNativePrerequisite{Backend: fields[0]}, OperationId: t.operationID(),
	})
	if err != nil {
		return "", fmt.Errorf("prepare %s: %w — /local prepare <%s> retries; /providers shows the local connection", fields[0], err, "llama|openvino")
	}
	out := fmt.Sprintf("Prepared %s for this computer.", fields[0])
	if id := result.GetProviderResult().GetId(); id != "" {
		out += " Local connection " + id + " is ready; /local models lists the models to download, /role <role> auto recommends one for a role."
	}
	return out, nil
}

func (t *inferenceHandler) handleLocalModels(ctx context.Context) (string, error) {
	acquisition, err := t.modelAcquisition(ctx)
	if err != nil {
		return "", fmt.Errorf("read the model catalog: %w", err)
	}
	library, err := acquisition.Library(ctx)
	if err != nil {
		return "", fmt.Errorf("read the model catalog: %w", err)
	}
	sort.Slice(library, func(i, j int) bool { return library[i].Model.Name < library[j].Model.Name })
	var b strings.Builder
	b.WriteString("Local models you can download:\n")
	for _, entry := range library {
		roles := "no role"
		if len(entry.Roles) > 0 {
			roles = "serves " + strings.Join(entry.Roles, ", ")
		}
		state := "not downloaded"
		if entry.Downloaded {
			state = "downloaded"
		}
		fmt.Fprintf(&b, "  %s — %s — %s — %s", entry.Model.Name, roles, formatBytes(entry.Model.SizeBytes+entry.Model.MMProjSizeBytes), state)
		if !entry.Model.AutoAcquirable() && entry.Model.License != "" {
			fmt.Fprintf(&b, " — %s licence: setup never downloads it on its own", entry.Model.License)
		}
		b.WriteString("\n")
	}
	b.WriteString("Download one with /local pull <model>; /role <role> <connection> <model> assigns a downloaded model.")
	return b.String(), nil
}

func (t *inferenceHandler) handleLocalPull(ctx context.Context, fields []string) (string, error) {
	if len(fields) != 1 {
		return "", errors.New("usage: /local pull <model> — /local models lists the models that can be downloaded")
	}
	model := fields[0]
	if _, err := t.runInference(ctx, &protocolv1.InferenceRequest{Action: "pull", Model: model, OperationId: t.operationID()}); err != nil {
		return "", fmt.Errorf("%s was not downloaded: %w", model, err)
	}
	snapshot, err := t.inferenceSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("read providers: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Downloaded %s.", model)
	connection, ok := localConnection(snapshot.Providers)
	if !ok {
		return b.String(), nil
	}
	acquisition, err := t.modelAcquisition(ctx)
	if err != nil {
		return "", fmt.Errorf("read the model catalog: %w", err)
	}
	library, err := acquisition.Library(ctx)
	if err != nil {
		return "", fmt.Errorf("read the model catalog: %w", err)
	}
	for _, entry := range library {
		if entry.Model.Name != model || len(entry.Roles) == 0 {
			continue
		}
		reference := connectionReference(snapshot.Providers, connection)
		b.WriteString(" It is available on " + connection.Name + "; assign it with:")
		for _, role := range entry.Roles {
			fmt.Fprintf(&b, "\n  /role %s %s %s", role, reference, model)
		}
	}
	return b.String(), nil
}

func localConnection(providers []inferencesetup.ProviderConnection) (inferencesetup.ProviderConnection, bool) {
	for _, provider := range providers {
		if nativeProvider(provider) && provider.URL == "local" {
			return provider, true
		}
	}
	return inferencesetup.ProviderConnection{}, false
}

func nativeProvider(provider inferencesetup.ProviderConnection) bool {
	return provider.Type == "modeld" || provider.Type == "local"
}

func connectionReference(providers []inferencesetup.ProviderConnection, connection inferencesetup.ProviderConnection) string {
	count := 0
	for _, provider := range providers {
		if provider.Type == connection.Type {
			count++
		}
	}
	if count == 1 {
		return connection.Type
	}
	return connection.Selector
}

func (t *inferenceHandler) handleLocalDevices(ctx context.Context) (string, error) {
	setup, err := autosetup.New(t.deps.DB, t.tracker())
	if err != nil {
		return "", err
	}
	roles := t.roles
	hosts, err := setup.Devices(ctx, roles)
	if err != nil {
		return "", fmt.Errorf("read devices: %w", err)
	}
	if len(hosts) == 0 {
		return "No local connection is saved. /local prepare <llama|openvino> sets up local inference.", nil
	}
	var b strings.Builder
	for _, host := range hosts {
		fmt.Fprintf(&b, "%s", host.Connection)
		if host.Error != "" {
			fmt.Fprintf(&b, " could not be read: %s\n", host.Error)
			continue
		}
		fmt.Fprintf(&b, " — %d CPU threads\n", host.HostThreads)
		for _, pool := range host.Pools {
			fmt.Fprintf(&b, "  %s (pool %s): %s free of %s", pool.Label, pool.ID, formatBytes(pool.AvailableBytes), formatBytes(pool.TotalBytes))
			if pool.Bounded {
				fmt.Fprintf(&b, "; local inference may use %d%%", int(pool.Share*100+0.5))
			} else {
				b.WriteString("; no limit set")
			}
			if pool.State != "available" {
				fmt.Fprintf(&b, "; %s", pool.State)
				if pool.Error != "" {
					fmt.Fprintf(&b, " (%s)", pool.Error)
				}
			}
			b.WriteString("\n")
			for _, device := range pool.Devices {
				fmt.Fprintf(&b, "    %s — %s, %s\n", device.Label, device.Backend, device.State)
			}
		}
		for _, device := range host.Unpooled {
			fmt.Fprintf(&b, "  %s — %s, %s (shares no memory pool)\n", device.Label, device.Backend, device.State)
		}
	}
	b.WriteString("Limit how much of a pool local inference may use with /local ceiling <connection> <pool> <percent>.")
	return b.String(), nil
}

func parsePercent(value string) (int, error) {
	percent, err := strconv.Atoi(strings.TrimSuffix(value, "%"))
	if err != nil || percent < 1 || percent > 100 {
		return 0, fmt.Errorf("%q is not a percentage from 1 to 100", value)
	}
	return percent, nil
}

func (t *inferenceHandler) handleLocalCeiling(ctx context.Context, fields []string) (string, error) {
	const usage = "usage: /local ceiling <connection> <pool> <percent> — /local devices lists the connections and their pools"
	if len(fields) != 3 {
		return "", errors.New(usage)
	}
	percent, err := parsePercent(fields[2])
	if err != nil {
		return "", fmt.Errorf("%w — %s", err, usage)
	}
	snapshot, err := t.inferenceSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("read providers: %w", err)
	}
	connection, ok := resolveConnection(snapshot.Providers, fields[0])
	if !ok {
		return "", unknownConnection(fields[0])
	}
	if !nativeProvider(connection) {
		return "", fmt.Errorf("%s is not a local connection — /local devices lists the local ones", connection.Name)
	}
	result, err := t.configureCeiling(ctx, autosetup.PoolCeiling{ConnectionID: connection.ID, PoolID: fields[1], Share: float64(percent) / 100})
	if err != nil {
		return "", fmt.Errorf("the limit was not changed: %w — /local devices lists the pools", err)
	}
	return renderCeiling(result, percent), nil
}

func renderCeiling(result autosetup.CeilingResult, percent int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Local inference may now use up to %d%% of %s on %s (%s of %s).", percent, result.PoolLabel, result.Connection, formatBytes(result.LimitBytes), formatBytes(result.TotalBytes))
	if len(result.Unfit) > 0 {
		fmt.Fprintf(&b, "\nThese assigned models no longer fit under that limit; together they hold %s:", formatBytes(result.HeldBytes))
		for _, held := range result.Unfit {
			fmt.Fprintf(&b, "\n  %s: %s holds %s — /role %s auto picks a model that fits", held.Name, held.Model, formatBytes(held.Bytes), held.Role)
		}
		b.WriteString("\nRaise the limit with /local ceiling to keep them as they are.")
	}
	if len(result.Unchecked) > 0 {
		b.WriteString("\nThese assigned models have not been verified, so how much memory they use is unknown and whether they fit was not checked:")
		for _, unchecked := range result.Unchecked {
			fmt.Fprintf(&b, "\n  %s: %s — /role %s %s %s verifies it", unchecked.Name, unchecked.Model, unchecked.Role, unchecked.Provider, unchecked.Model)
		}
	}
	return b.String()
}

func (t *inferenceHandler) handleCompletion(ctx context.Context, fields []string) (string, error) {
	modes := strings.Join([]string{modelregistry.CompletionInstruct, modelregistry.CompletionPrompt, modelregistry.CompletionFIM}, "|")
	usage := "usage: /completion <connection> <model> " + modes + " <format> [context] — format applies to fim only, one of " + strings.Join(modelcontract.FIMFormats(), ", ") + "; /roles shows the models in use"
	if len(fields) < 3 {
		return "", errors.New(usage)
	}
	reference, model, mode := fields[0], fields[1], fields[2]
	if !slices.Contains([]string{modelregistry.CompletionInstruct, modelregistry.CompletionPrompt, modelregistry.CompletionFIM}, mode) {
		return "", fmt.Errorf("unknown completion mode %q — %s", mode, usage)
	}
	declaration := inferencesetup.CompletionDeclaration{Model: model, Mode: mode}
	extra := fields[3:]
	if mode == modelregistry.CompletionFIM {
		if len(extra) < 1 || len(extra) > 2 {
			return "", errors.New(usage)
		}
		declaration.FIMFormat = extra[0]
		if len(extra) == 2 {
			tokens, err := parseTokenCount(extra[1])
			if err != nil {
				return "", fmt.Errorf("%w — %s", err, usage)
			}
			declaration.ContextLength = tokens
		}
	} else if len(extra) != 0 {
		return "", fmt.Errorf("%s takes no format or context — %s", mode, usage)
	}
	snapshot, err := t.inferenceSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("read providers: %w", err)
	}
	connection, ok := resolveConnection(snapshot.Providers, reference)
	if !ok {
		return "", fmt.Errorf("the completion contract was not declared: %w", unknownConnection(reference))
	}
	declaration.Provider = connection.Selector
	setup := t.roles
	if err := setup.DeclareCompletionContract(ctx, declaration); err != nil {
		var needsContext *inferencesetup.ContextLengthRequiredError
		var unavailable *inferencesetup.ConnectionUnavailableError
		switch {
		case errors.As(err, &needsContext):
			return "", fmt.Errorf("the completion contract was not declared: %w — state it as the last word: /completion %s %s %s %s <context tokens>", err, reference, model, mode, declaration.FIMFormat)
		case errors.As(err, &unavailable):
			return "", fmt.Errorf("the completion contract was not declared: %w — /providers shows whether the provider is usable", err)
		}
		return "", fmt.Errorf("the completion contract was not declared: %w", err)
	}
	out := fmt.Sprintf("Declared %s on %s as %s completion", model, reference, mode)
	if mode == modelregistry.CompletionFIM {
		out += " in the " + declaration.FIMFormat + " format"
	}
	return out + ". /role autocomplete " + reference + " " + model + " assigns it and verifies it with a real completion.", nil
}

func parseTokenCount(value string) (int, error) {
	multiplier := 1
	switch {
	case strings.HasSuffix(value, "k"), strings.HasSuffix(value, "K"):
		multiplier, value = 1_000, value[:len(value)-1]
	case strings.HasSuffix(value, "m"), strings.HasSuffix(value, "M"):
		multiplier, value = 1_000_000, value[:len(value)-1]
	}
	count, err := strconv.Atoi(value)
	if err != nil || count < 1 {
		return 0, fmt.Errorf("%q is not a token count such as 8192 or 32k", value)
	}
	if count > math.MaxInt/multiplier {
		return 0, fmt.Errorf("token count %q is too large", value)
	}
	return count * multiplier, nil
}

func assignmentRefusal(role, reference string, err error) error {
	var missing *inferencesetup.ModelNotDiscoveredError
	var contract *inferencesetup.CompletionContractMissingError
	var unavailable *inferencesetup.ConnectionUnavailableError
	switch {
	case errors.As(err, &missing) && missing.Curated:
		return fmt.Errorf("%s was not assigned: %s is not downloaded on %s — /local pull %s downloads it, then /role %s %s %s assigns it", role, missing.Model, reference, missing.Model, role, reference, missing.Model)
	case errors.As(err, &missing):
		return fmt.Errorf("%s was not assigned: %s does not offer a model named %q — /providers shows the connection's state", role, reference, missing.Model)
	case errors.As(err, &contract):
		return fmt.Errorf("%s was not assigned: %s on %s has no code-completion contract — /completion %s %s %s <format> declares it", role, contract.Model, reference, reference, contract.Model, strings.Join([]string{modelregistry.CompletionInstruct, modelregistry.CompletionPrompt, modelregistry.CompletionFIM}, "|"))
	case errors.As(err, &unavailable):
		return fmt.Errorf("%s was not assigned: %w — /providers shows whether the provider is usable; contenox-runtime backend list shows its saved endpoint", role, err)
	}
	return fmt.Errorf("%s was not assigned: %w", role, err)
}

func (t *inferenceHandler) operationID() string {
	if t.conn == nil {
		return ""
	}
	return uuid.NewString()
}

func (t *inferenceHandler) runInference(ctx context.Context, request *protocolv1.InferenceRequest) (*protocolv1.InferenceResult, error) {
	answer, rpcErr := t.run(ctx, request)
	if rpcErr != nil {
		return nil, errors.New(rpcErr.Message)
	}
	result := &protocolv1.InferenceResult{}
	if err := protocolRequest.Unmarshal(answer, result); err != nil {
		return nil, fmt.Errorf("decode inference answer: %w", err)
	}
	return result, nil
}

// resolveConnection matches a connection by id, selector or name, and by type only when exactly one connection has that type.
func resolveConnection(providers []inferencesetup.ProviderConnection, reference string) (inferencesetup.ProviderConnection, bool) {
	for _, provider := range providers {
		if reference == provider.ID || reference == provider.Selector || reference == provider.Name {
			return provider, true
		}
	}
	var typed []inferencesetup.ProviderConnection
	for _, provider := range providers {
		if reference == provider.Type {
			typed = append(typed, provider)
		}
	}
	if len(typed) == 1 {
		return typed[0], true
	}
	return inferencesetup.ProviderConnection{}, false
}

func unknownConnection(reference string) error {
	return fmt.Errorf("no connection %q — /providers lists the connected providers; use contenox-runtime backend add to connect another", reference)
}

func (t *inferenceHandler) handleProviders(ctx context.Context) (string, error) {
	snapshot, err := t.inferenceSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("read providers: %w", err)
	}
	if len(snapshot.Providers) == 0 {
		types := make([]string, 0, len(snapshot.ProviderTypes))
		for _, kind := range snapshot.ProviderTypes {
			types = append(types, kind.Provider)
		}
		return "No provider is connected. Connect one with contenox-runtime backend add; local models: /local prepare <llama|openvino>.\nTypes: " + strings.Join(types, ", "), nil
	}
	var b strings.Builder
	b.WriteString("Connected providers:\n")
	for _, provider := range snapshot.Providers {
		fmt.Fprintf(&b, "  %s (%s) — %s", provider.Name, provider.Type, provider.State)
		if provider.AuthorizationState != "" {
			fmt.Fprintf(&b, "; credential %s", provider.AuthorizationState)
		}
		fmt.Fprintf(&b, "; discovery %s", provider.DiscoveryState)
		if provider.Error != "" {
			fmt.Fprintf(&b, "\n    %s", provider.Error)
		}
		if provider.AuthorizationError != "" {
			fmt.Fprintf(&b, "\n    %s", provider.AuthorizationError)
		}
		b.WriteString("\n")
	}
	b.WriteString("Assign a role with /role <role> <provider> <model>; /roles shows what is assigned.")
	return b.String(), nil
}

func (t *inferenceHandler) handleRoles(ctx context.Context) (string, error) {
	snapshot, err := t.inferenceSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("read roles: %w", err)
	}
	var b strings.Builder
	b.WriteString("AI roles:\n")
	for _, role := range snapshot.Roles {
		b.WriteString(renderRole(role))
		b.WriteString("\n")
	}
	b.WriteString("Assign with /role <role> <provider> <model>, or /role <role> auto to recommend one.")
	return b.String(), nil
}

func renderRole(role inferenceRole) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %s (%s) — %s", role.ID, role.Name, role.State)
	if role.Provider != "" && role.Model != "" {
		fmt.Fprintf(&b, "; %s/%s", role.Provider, role.Model)
	}
	if role.AssignmentState != "" {
		fmt.Fprintf(&b, "; %s", role.AssignmentState)
	}
	if role.Inherited {
		b.WriteString("; inherited")
	}
	if !role.Enabled {
		b.WriteString("; disabled")
	}
	if role.Detail != "" {
		fmt.Fprintf(&b, "\n    %s", role.Detail)
	}
	if role.Problem != "" {
		fmt.Fprintf(&b, "\n    %s", role.Problem)
	}
	if role.Provider == "" || role.Model == "" {
		fmt.Fprintf(&b, "\n    not assigned — /role %s <provider> <model> or /role %s auto", role.ID, role.ID)
	}
	return b.String()
}

func roleUsage() string {
	return "usage: /role <role> <provider> <model> | /role <role> auto | /role <role> on|off — roles: " + strings.Join(inferencesetup.RoleIDs(), ", ") + "; /providers lists the providers, /roles the current assignments"
}

func (t *inferenceHandler) handleRole(ctx context.Context, fields []string) (string, error) {
	if len(fields) < 2 {
		return "", errors.New(roleUsage())
	}
	role := fields[0]
	if !slices.Contains(inferencesetup.RoleIDs(), role) {
		return "", fmt.Errorf("unknown role %q — roles: %s; /roles shows their state", role, strings.Join(inferencesetup.RoleIDs(), ", "))
	}
	if len(fields) == 2 && fields[1] == "auto" {
		return t.handleRoleAuto(ctx, role)
	}
	if len(fields) == 2 && (fields[1] == "on" || fields[1] == "off") {
		return t.handleRoleEnabled(ctx, role, fields[1] == "on")
	}
	if len(fields) != 3 {
		return "", errors.New(roleUsage())
	}
	snapshot, err := t.inferenceSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("read providers: %w", err)
	}
	connection, ok := resolveConnection(snapshot.Providers, fields[1])
	if !ok {
		return "", fmt.Errorf("%s was not assigned: %w", role, unknownConnection(fields[1]))
	}
	if err := t.assignInferenceRole(ctx, role, connection.Selector, fields[2], false); err != nil {
		return "", assignmentRefusal(role, fields[1], err)
	}
	return t.renderAssignment(ctx, role)
}

func (t *inferenceHandler) handleRoleEnabled(ctx context.Context, role string, enabled bool) (string, error) {
	state := "off"
	if enabled {
		state = "on"
	}
	if _, err := t.runInference(ctx, &protocolv1.InferenceRequest{Action: "set-role-enabled", Role: role, Enabled: enabled}); err != nil {
		return "", fmt.Errorf("%s was not turned %s: %w", role, state, err)
	}
	snapshot, err := t.inferenceSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("read roles: %w", err)
	}
	for _, current := range snapshot.Roles {
		if current.ID == role {
			return fmt.Sprintf("Turned %s %s.\n%s", role, state, renderRole(current)), nil
		}
	}
	return "", fmt.Errorf("role %q is missing from the configuration", role)
}

func (t *inferenceHandler) renderAssignment(ctx context.Context, role string) (string, error) {
	snapshot, err := t.inferenceSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("read roles: %w", err)
	}
	for _, current := range snapshot.Roles {
		if current.ID != role {
			continue
		}
		out := fmt.Sprintf("Assigned %s to %s/%s.", role, current.Provider, current.Model)
		if current.Verification != nil {
			out += fmt.Sprintf(" Verified by a real request at %s", current.Verification.VerifiedAt.Format("15:04:05"))
			if sample := strings.TrimSpace(current.Verification.Sample); sample != "" {
				out += ": " + truncateText(sample, 120)
			} else {
				out += "."
			}
		}
		return out, nil
	}
	return "", fmt.Errorf("role %q is missing from the configuration read after assignment — /roles shows the roles", role)
}

func truncateText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func (t *inferenceHandler) handleRoleAuto(ctx context.Context, role string) (string, error) {
	snapshot, err := t.inferenceSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("read roles: %w", err)
	}
	if len(snapshot.Providers) == 0 {
		return "", errors.New("no provider is connected to recommend from — /local prepare <llama|openvino> sets up local models, use contenox-runtime backend add to connect an endpoint")
	}
	inputs := string(inferencesetup.InputText)
	for _, current := range snapshot.Roles {
		if current.ID == role && current.Inputs != "" {
			inputs = string(current.Inputs)
		}
	}
	allowed := make([]string, 0, len(snapshot.Providers))
	for _, provider := range snapshot.Providers {
		allowed = append(allowed, provider.ID)
	}
	recommended, err := t.runInference(ctx, &protocolv1.InferenceRequest{
		Action: "auto-recommend",
		Recommendation: &protocolv1.InferenceRecommendationRequest{
			Mode: "mixed",
			Constraints: []*protocolv1.InferenceRoleConstraint{{
				Role: role, Policy: "allowed", AllowedConnections: allowed, Inputs: inputs, ExecutionMode: "protected",
			}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("recommend %s: %w — /providers shows the connections the recommendation can use", role, err)
	}
	plan := recommended.GetAutomaticSetup().GetPlan()
	var planned *protocolv1.InferenceRolePlan
	for _, candidate := range plan.GetRoles() {
		if candidate.GetConstraint().GetRole() == role {
			planned = candidate
		}
	}
	if planned == nil {
		return "", fmt.Errorf("the planner returned no proposal for %s — /roles shows its state", role)
	}
	selected := planned.GetSelected()
	if selected == nil || planned.GetState() != "review" && planned.GetState() != "acquire" {
		repairs := strings.Join(planned.GetRepairs(), "; ")
		if repairs == "" {
			repairs = "the role is " + planned.GetState()
		}
		return "", fmt.Errorf("no recommendation can be applied to %s: %s — /roles shows its state, /local prepare <llama|openvino> or connect an endpoint with contenox-runtime backend add", role, repairs)
	}
	sentence := recommendationSentence(role, planned)
	if _, err := t.runInference(ctx, &protocolv1.InferenceRequest{
		Action: "auto-apply", Plan: plan, SelectedRoles: []string{role}, OperationId: t.operationID(),
	}); err != nil {
		return "", fmt.Errorf("%s\n%s was not applied: %w — /roles shows its state; /providers shows the connections", sentence, role, err)
	}
	assigned, err := t.renderAssignment(ctx, role)
	if err != nil {
		return "", err
	}
	return sentence + "\n" + assigned, nil
}

func recommendationSentence(role string, planned *protocolv1.InferenceRolePlan) string {
	selected := planned.GetSelected()
	route := selected.GetRoute()
	var b strings.Builder
	fmt.Fprintf(&b, "Recommended for %s: %s/%s", role, route.GetProvider(), route.GetModel())
	switch {
	case selected.GetInstalled():
		b.WriteString("; installed")
	case selected.GetArtifact().GetSizeBytes() > 0:
		fmt.Fprintf(&b, "; download %s", formatBytes(selected.GetArtifact().GetSizeBytes()+selected.GetArtifact().GetMmprojSizeBytes()))
	case planned.GetState() == "acquire":
		b.WriteString("; needs a download")
	default:
		b.WriteString("; hosted")
	}
	for _, pool := range selected.GetPools() {
		fmt.Fprintf(&b, "; device %s %s, %s of memory", pool.GetHost(), pool.GetPoolId(), formatBytes(pool.GetBytes()))
	}
	b.WriteString(".")
	return b.String()
}

func formatBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	div, exp := int64(unit), 0
	for n := value / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(div), "KMGTPE"[exp])
}

func (t *Transport) dispatchInferenceCommand(ctx context.Context, command string, fields []string) (string, error) {
	if t.deps.OpenInference == nil {
		return "", errors.New("inference configuration is unavailable")
	}
	setup, state, closeSetup, err := t.deps.OpenInference(ctx)
	if err != nil {
		return "", err
	}
	defer closeSetup()
	handler := inferenceHandler{Transport: t, roles: setup, state: state}
	switch command {
	case "providers":
		return handler.handleProviders(ctx)
	case "roles":
		return handler.handleRoles(ctx)
	case "role":
		return handler.handleRole(ctx, fields)
	case "completion":
		return handler.handleCompletion(ctx, fields)
	case "local":
		return handler.handleLocal(ctx, fields)
	default:
		return "", fmt.Errorf("unknown inference command %q", command)
	}
}

func (t *Transport) setInferenceCompletions(commands []libacp.AvailableCommand) {
	ctx := t.connCtx
	if ctx == nil {
		ctx = context.Background()
	}
	var names, native []string
	if t.deps.DB != nil {
		backends, err := runtimetypes.New(t.deps.DB.WithoutTransaction()).ListAllBackends(ctx)
		if err == nil {
			counts := map[string]int{}
			for _, backend := range backends {
				names = append(names, backend.Name)
				counts[backend.Type]++
			}
			for _, backend := range backends {
				if backend.Type == "modeld" || backend.Type == "local" {
					reference := backend.Type
					if counts[backend.Type] > 1 {
						reference = runtimestate.ConnectionSelector(backend.ID)
					}
					native = append(native, reference)
				}
			}
		}
	}
	var models []string
	curated, _ := modelregistry.NewCurated().List(ctx)
	for _, model := range curated {
		models = append(models, model.Name)
	}
	sort.Strings(models)
	for i, command := range commands {
		domains := map[string][]string{}
		switch command.Name {
		case "local":
			domains[""] = []string{"prepare", "models", "pull", "devices", "ceiling"}
			domains["prepare"] = []string{"llama", "openvino"}
			domains["pull"], domains["ceiling"] = models, native
		case "role":
			domains[""] = inferencesetup.RoleIDs()
			for _, role := range domains[""] {
				domains[role] = append([]string{"auto", "on", "off"}, names...)
			}
		case "completion":
			domains[""] = names
		default:
			continue
		}
		meta := libacp.CommandCompletionMeta{Completions: map[string]libacp.CommandCompletion{}}
		for key, values := range domains {
			meta.Completions[key] = libacp.CommandCompletion{Values: values, Hint: "/" + command.Name + " " + command.Input.Hint}
		}
		commands[i].Meta, _ = json.Marshal(meta)
	}
}
