package hitlservice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"
)

type Action string

const (
	ActionAllow   Action = "allow"
	ActionApprove Action = "approve"
	ActionDeny    Action = "deny"
)

type ApprovalRequest struct {
	ToolCallID string
	ToolsName  string
	ToolName   string
	Args       map[string]any
	Diff       string
	DiffOld    string
	DiffNew    string

	PolicyName  string
	MatchedRule *int
	TimeoutS    int
	OnTimeout   Action

	InstanceID string
	SessionID  string
	AgentName  string
	MissionID  string
}

type ConditionOp string

const (
	OpEq                    ConditionOp = "eq"
	OpGlob                  ConditionOp = "glob"
	OpHost                  ConditionOp = "host"
	OpCommandBlacklist      ConditionOp = "command_blacklist"
	OpCommandAskAlways      ConditionOp = "command_ask_always"
	OpNoCommandSubstitution ConditionOp = "no_command_substitution"
)

type Condition struct {
	Key   string      `json:"key"`
	Op    ConditionOp `json:"op"`
	Value string      `json:"value"`
}

type Rule struct {
	Tools     string      `json:"tools"`
	Tool      string      `json:"tool"`
	When      []Condition `json:"when,omitempty"`
	Action    Action      `json:"action"`
	TimeoutS  int         `json:"timeout_s,omitempty"`
	OnTimeout Action      `json:"on_timeout,omitempty"`
}

type Policy struct {
	DefaultAction Action           `json:"default_action,omitempty"`
	Rules         []Rule           `json:"rules"`
	Compute       *ComputeBounds   `json:"compute,omitempty"`
	Attention     *AttentionBounds `json:"attention,omitempty"`
}

type OnExhausted string

const (
	OnExhaustedFinishStuck OnExhausted = "finish_stuck"
	OnExhaustedPauseAsk    OnExhausted = "pause_ask"
)

type ComputeBounds struct {
	MaxTurns         int         `json:"maxTurns,omitempty"`
	MaxToolCalls     int         `json:"maxToolCalls,omitempty"`
	MaxTokens        int         `json:"maxTokens,omitempty"`
	ModelAllowlist   []string    `json:"modelAllowlist,omitempty"`
	BackendAllowlist []string    `json:"backendAllowlist,omitempty"`
	OnExhausted      OnExhausted `json:"onExhausted,omitempty"`
}

const (
	maxComputeTurns               = 100_000
	maxComputeToolCalls           = 10_000_000
	maxComputeTokens              = 100_000_000_000
	maxComputeAllowlist           = 256
	maxComputeAllowlistEntryBytes = 512
)

const (
	ReasonMatchedRule   = "matched_rule"
	ReasonDefaultAction = "default_action"
)

type EvaluationResult struct {
	Action      Action
	MatchedRule *int
	Reason      string
	TimeoutS    int
	OnTimeout   Action
	PolicyName  string
}

func evaluate(p *Policy, toolsName, toolName string, args map[string]any) EvaluationResult {
	for i, r := range p.Rules {
		if ruleMatches(r, toolsName, toolName, args) {
			idx := i
			return EvaluationResult{
				Action:      r.Action,
				MatchedRule: &idx,
				Reason:      ReasonMatchedRule,
				TimeoutS:    r.TimeoutS,
				OnTimeout:   r.OnTimeout,
			}
		}
	}
	defaultAction := p.DefaultAction
	if defaultAction == "" {
		defaultAction = ActionApprove
	}
	return EvaluationResult{
		Action: defaultAction,
		Reason: ReasonDefaultAction,
	}
}

func ruleMatches(r Rule, toolsName, toolName string, args map[string]any) bool {
	toolsOK := r.Tools == "" || r.Tools == "*" || r.Tools == toolsName
	toolOK := r.Tool == "" || r.Tool == "*" || r.Tool == toolName
	if !toolsOK || !toolOK {
		return false
	}
	for _, c := range r.When {
		if !conditionMatches(c, args) {
			return false
		}
	}
	return true
}

func conditionMatches(c Condition, args map[string]any) bool {
	val, ok := args[c.Key]
	if !ok {
		return false
	}
	for _, s := range conditionValues(val) {
		switch c.Op {
		case OpEq:
			if s == c.Value {
				return true
			}
		case OpGlob:
			if globMatch(c.Value, s) {
				return true
			}
		case OpHost:
			if urlHostMatches(s, c.Value) {
				return true
			}
		case OpCommandBlacklist:
			if isCommandBlacklisted(args, c.Value) {
				return true
			}
		case OpCommandAskAlways:
			if isCommandInAskAlwaysList(args, c.Value) {
				return true
			}
		case OpNoCommandSubstitution:
			if detectCommandSubstitution(args) {
				return true
			}
		}
	}
	return false
}

func urlHostMatches(rawURL, patternsCSV string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	isIP := net.ParseIP(host) != nil
	for _, p := range strings.Split(patternsCSV, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if host == p {
			return true
		}
		if !isIP && strings.HasSuffix(host, "."+p) {
			return true
		}
	}
	return false
}

func conditionValues(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			out = append(out, fmt.Sprintf("%v", e))
		}
		return out
	default:
		return []string{fmt.Sprintf("%v", v)}
	}
}

func globMatch(pattern, s string) bool {
	s = path.Clean(s)
	for _, expanded := range expandGlobBraces(pattern) {
		if globMatchOne(expanded, s) {
			return true
		}
	}
	return false
}

func globMatchOne(pattern, s string) bool {
	pattern = path.Clean(pattern)
	if !strings.ContainsAny(pattern, "*?[") {
		return pattern == s
	}
	if !strings.Contains(pattern, "**") {
		matched, err := path.Match(pattern, s)
		return err == nil && matched
	}
	return matchDoubleGlob(pattern, s)
}

const maxGlobExpansions = 4096

func expandGlobBraces(pattern string) []string {
	const maxExpansions = maxGlobExpansions
	out := []string{pattern}
	for {
		next := make([]string, 0, len(out))
		changed := false
		for _, p := range out {
			open, closeIdx, ok := firstBracePair(p)
			if !ok {
				next = append(next, p)
				continue
			}
			changed = true
			prefix, suffix := p[:open], p[closeIdx+1:]
			for _, alt := range splitTopLevelCommas(p[open+1 : closeIdx]) {
				next = append(next, prefix+alt+suffix)
			}
		}
		if !changed {
			return next
		}
		if len(next) > maxExpansions {
			return next[:maxExpansions]
		}
		out = next
	}
}

func firstBracePair(p string) (open, closeIdx int, ok bool) {
	for i := 0; i < len(p); i++ {
		if p[i] != '{' {
			continue
		}
		if c, found := matchingBrace(p, i); found {
			return i, c, true
		}
	}
	return 0, 0, false
}

func matchingBrace(s string, open int) (int, bool) {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return -1, false
}

func splitTopLevelCommas(s string) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, s[start:])
}

func matchDoubleGlob(pattern, s string) bool {
	idx := strings.Index(pattern, "**")
	prefix := strings.TrimSuffix(pattern[:idx], "/")
	after := strings.TrimPrefix(pattern[idx+2:], "/")

	if prefix != "" {
		if s == prefix {
			return after == ""
		}
		if !strings.HasPrefix(s, prefix+"/") {
			return false
		}
		s = s[len(prefix)+1:]
	}

	if after == "" {
		return true
	}

	for {
		if matchSuffix(after, s) {
			return true
		}
		slash := strings.Index(s, "/")
		if slash < 0 {
			break
		}
		s = s[slash+1:]
	}
	return false
}

func matchSuffix(pattern, s string) bool {
	if !strings.Contains(pattern, "**") {
		matched, err := path.Match(pattern, s)
		return err == nil && matched
	}
	return matchDoubleGlob(pattern, s)
}

var commandSubstitutionPatterns = []string{
	"$(",
	"`",
	"<(",
	">(",
	"$[",
	"${}",
	"$((",
}

func detectCommandSubstitution(args map[string]any) bool {
	for _, v := range args {
		for _, s := range conditionValues(v) {
			for _, pattern := range commandSubstitutionPatterns {
				if strings.Contains(s, pattern) {
					return true
				}
			}
		}
	}
	return false
}

func getCommandFromArgs(args map[string]any) string {
	if cmd, ok := args["command"].(string); ok {
		return cmd
	}
	if argStr, ok := args["args"].(string); ok {
		parts := strings.Fields(argStr)
		if len(parts) > 0 {
			return parts[0]
		}
	}
	if argList, ok := args["args"].([]string); ok && len(argList) > 0 {
		return argList[0]
	}
	return ""
}

func isCommandBlacklisted(args map[string]any, blacklist string) bool {
	if blacklist == "" {
		return false
	}
	cmd := getCommandFromArgs(args)
	if cmd == "" {
		return false
	}
	base := path.Base(cmd)
	base = strings.Fields(base)[0]
	if base == "" {
		return false
	}
	for _, denied := range strings.Split(blacklist, ",") {
		denied = strings.TrimSpace(denied)
		if denied == "" {
			continue
		}
		if base == denied {
			return true
		}
	}
	return false
}

func isCommandInAskAlwaysList(args map[string]any, commandList string) bool {
	if commandList == "" {
		return false
	}
	cmd := getCommandFromArgs(args)
	if cmd == "" {
		return false
	}
	base := path.Base(cmd)
	base = strings.Fields(base)[0]
	if base == "" {
		return false
	}
	for _, cmdName := range strings.Split(commandList, ",") {
		cmdName = strings.TrimSpace(cmdName)
		if cmdName == "" {
			continue
		}
		if base == cmdName {
			return true
		}
	}
	return false
}

func loadPolicy(ctx context.Context, src PolicySource, tenantID, policyPath string) (*Policy, error) {
	data, err := src.ReadPolicy(ctx, tenantID, policyPath)
	if err != nil {
		return nil, fmt.Errorf("read hitl policy %q: %w", policyPath, err)
	}
	var p Policy
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse hitl policy %q: %w", policyPath, err)
	}
	if err := rejectUnknownComputeFields(data); err != nil {
		return nil, fmt.Errorf("invalid hitl policy %q: %w", policyPath, err)
	}
	if err := validatePolicy(&p); err != nil {
		return nil, fmt.Errorf("invalid hitl policy %q: %w", policyPath, err)
	}
	return &p, nil
}

func rejectUnknownComputeFields(data []byte) error {
	var probe struct {
		Compute json.RawMessage `json:"compute"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil
	}
	if len(probe.Compute) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(probe.Compute))
	dec.DisallowUnknownFields()
	var cb ComputeBounds
	if err := dec.Decode(&cb); err != nil {
		return fmt.Errorf("compute: %w", err)
	}
	return nil
}

func validatePolicy(p *Policy) error {
	validActions := map[Action]bool{ActionAllow: true, ActionApprove: true, ActionDeny: true}
	if p.DefaultAction != "" && !validActions[p.DefaultAction] {
		return fmt.Errorf("unknown default_action %q", p.DefaultAction)
	}
	for i, r := range p.Rules {
		if !validActions[r.Action] {
			return fmt.Errorf("rule %d: unknown action %q", i, r.Action)
		}
		if r.OnTimeout == ActionAllow {
			return fmt.Errorf("rule %d: on_timeout=%q is not permitted (would silently bypass approval)", i, ActionAllow)
		}
		if r.OnTimeout != "" && !validActions[r.OnTimeout] {
			return fmt.Errorf("rule %d: unknown on_timeout %q", i, r.OnTimeout)
		}
		for j, c := range r.When {
			switch c.Op {
			case OpEq, OpHost, OpCommandBlacklist, OpCommandAskAlways, OpNoCommandSubstitution:
			case OpGlob:
				if err := validateGlobValue(c.Value); err != nil {
					return fmt.Errorf("rule %d, condition %d: %w", i, j, err)
				}
			default:
				return fmt.Errorf("rule %d, condition %d: unknown op %q", i, j, c.Op)
			}
		}
	}
	if p.Attention != nil {
		if err := validateAttentionBounds(p.Attention); err != nil {
			return err
		}
	}
	if p.Compute != nil {
		if err := validateComputeBounds(p.Compute); err != nil {
			return err
		}
	}
	return nil
}

func validateComputeBounds(c *ComputeBounds) error {
	if err := validateComputeCeiling("maxTurns", c.MaxTurns, maxComputeTurns); err != nil {
		return err
	}
	if err := validateComputeCeiling("maxToolCalls", c.MaxToolCalls, maxComputeToolCalls); err != nil {
		return err
	}
	if err := validateComputeCeiling("maxTokens", c.MaxTokens, maxComputeTokens); err != nil {
		return err
	}
	switch c.OnExhausted {
	case "", OnExhaustedFinishStuck, OnExhaustedPauseAsk:
	default:
		return fmt.Errorf("compute: unknown onExhausted %q (must be finish_stuck or pause_ask)", c.OnExhausted)
	}
	if err := validateComputeAllowlist("modelAllowlist", c.ModelAllowlist); err != nil {
		return err
	}
	return validateComputeAllowlist("backendAllowlist", c.BackendAllowlist)
}

func validateComputeCeiling(name string, v, max int) error {
	if v < 0 {
		return fmt.Errorf("compute: %s must not be negative (got %d)", name, v)
	}
	if v > max {
		return fmt.Errorf("compute: %s is out of range (got %d, max %d)", name, v, max)
	}
	return nil
}

func validateComputeAllowlist(name string, entries []string) error {
	if len(entries) > maxComputeAllowlist {
		return fmt.Errorf("compute: %s has too many entries (%d, max %d)", name, len(entries), maxComputeAllowlist)
	}
	for i, e := range entries {
		if strings.TrimSpace(e) == "" {
			return fmt.Errorf("compute: %s entry %d is empty", name, i)
		}
		if len(e) > maxComputeAllowlistEntryBytes {
			return fmt.Errorf("compute: %s entry %d exceeds max length (%d bytes, max %d)", name, i, len(e), maxComputeAllowlistEntryBytes)
		}
	}
	return nil
}

func validateGlobValue(value string) error {
	depth := 0
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return fmt.Errorf("unbalanced '}' in glob %q", value)
			}
		}
	}
	if depth != 0 {
		return fmt.Errorf("unbalanced '{' in glob %q", value)
	}
	if len(expandGlobBraces(value)) >= maxGlobExpansions {
		return fmt.Errorf("glob %q expands past the %d-pattern limit", value, maxGlobExpansions)
	}
	return nil
}

func secretDenyRules() []Rule {
	const allTools = "*"
	q := func(g string) Rule {
		return Rule{Tools: "local_fs", Tool: allTools, Action: ActionDeny, When: []Condition{{Key: "path", Op: OpGlob, Value: g}}}
	}
	w := func(g string) []Rule {
		return []Rule{
			{Tools: "local_fs", Tool: "write_file", Action: ActionDeny, When: []Condition{{Key: "path", Op: OpGlob, Value: g}}},
			{Tools: "local_fs", Tool: "sed", Action: ActionDeny, When: []Condition{{Key: "path", Op: OpGlob, Value: g}}},
		}
	}
	rules := []Rule{
		q("**/{.ssh,.gnupg,.aws,.azure,.kube,.config/gcloud,.config/doctl,.oci}/**"),
		q("**/{.password-store,.local/share/keyrings,Library/Keychains,.config/1Password,.config/Bitwarden,.config/keepassxc}/**"),
		q("**/{.electrum,.bitcoin,.ethereum/keystore}/**"),
		q("**/{wallet.dat,*.kdbx}"),
		q("**/{.mozilla,.config/google-chrome,.config/chromium,.config/BraveSoftware}/**"),
		q("**/Library/Application Support/{Google/Chrome,Firefox,BraveSoftware}/**"),
		q("**/{.bash_history,.zsh_history,.python_history,.netrc,.git-credentials,.npmrc,.pypirc}"),
		q("**/.docker/config.json"),
		q("**/{id_rsa,id_dsa,id_ecdsa,id_ed25519}*"),
	}
	rules = append(rules, w("**/{.ssh,.gnupg}/**")...)
	rules = append(rules, w("**/{.config/autostart,.config/systemd/user,Library/LaunchAgents,Library/LaunchDaemons}/**")...)
	rules = append(rules, w("**/{.bashrc,.zshrc,.profile,.bash_profile,.zprofile,.bash_login,.zshenv,.kshrc,crontab,.crontab}")...)
	rules = append(rules, w("**/.config/fish/config.fish")...)
	rules = append(rules, w("/{etc,usr,bin,sbin,boot,lib,lib64,opt,System}/**")...)
	rules = append(rules, w("**/hitl-policy*.json")...)
	return rules
}

func defaultPolicy() *Policy {
	return &Policy{
		DefaultAction: ActionApprove,
		Rules: append(secretDenyRules(), []Rule{
			{Tools: "local_fs", Tool: "read_file", Action: ActionAllow},
			{Tools: "local_fs", Tool: "read_file_range", Action: ActionAllow},
			{Tools: "local_fs", Tool: "list_dir", Action: ActionAllow},
			{Tools: "local_fs", Tool: "grep", Action: ActionAllow},
			{Tools: "local_fs", Tool: "stat_file", Action: ActionAllow},
			{Tools: "local_fs", Tool: "count_stats", Action: ActionAllow},
			{Tools: "local_fs", Tool: "write_file", Action: ActionApprove},
			{Tools: "local_fs", Tool: "sed", Action: ActionApprove},
			{Tools: "local_shell", Tool: "local_shell", Action: ActionDeny, When: []Condition{{Key: "command", Op: OpCommandBlacklist, Value: "mkfs,mke2fs,fdisk,shred,wipefs"}}},
			{Tools: "local_shell", Tool: "local_shell", Action: ActionApprove, When: []Condition{{Key: "command", Op: OpCommandAskAlways, Value: "rm,sudo,dd,chmod,chown,mv,cp,>:,>>"}}},
			{Tools: "local_shell", Tool: "local_shell", Action: ActionApprove, When: []Condition{{Key: "args", Op: OpNoCommandSubstitution, Value: ""}}},
			{Tools: "local_shell", Tool: "local_shell", Action: ActionApprove},
			{Tools: "shell_session", Tool: "shell_session_read", Action: ActionAllow},
			{Tools: "webtools", Tool: "web_get", Action: ActionAllow},
			{Tools: "webtools", Tool: "web_head", Action: ActionAllow},
			{Tools: "webtools", Tool: "web_post", Action: ActionApprove},
			{Tools: "webtools", Tool: "web_put", Action: ActionApprove},
			{Tools: "webtools", Tool: "web_patch", Action: ActionApprove},
			{Tools: "webtools", Tool: "web_delete", Action: ActionApprove},
			{Tools: "echo", Action: ActionAllow},
			{Tools: "print", Action: ActionAllow},
		}...),
	}
}
