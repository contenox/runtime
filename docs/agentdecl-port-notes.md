# Agent declarations and envelopes: compatibility audit and port plan

Status: B2 of `plans/2026-10-30-tool-fixes-agents-envelopes-pickers.md`, written after reading the enterprise package and the current runtime types. Nothing here is implemented.

Source: `enterprise/runtime/internal/services/agentdecl/` (read-only material). Target: `runtime/runtime/`.

## 1. Verdict

The chain format needs **no engine change**: every field `emit.go` writes already exists in `runtime/runtime/taskengine` with the same name and shape. The HITL policy format needs **three additions** to `hitlservice` — `version`, `trusted_binaries`, and the `command_prefix_allowlist` condition operator — because the shipped envelopes use them and the current evaluator does not know them. Everything else is a port of behaviour, not a format change.

## 2. Chain format

Verified field by field against `runtime/runtime/taskengine/tasktype.go`.

| Emitted by `emit.go` | Current runtime | Decision |
|---|---|---|
| `TaskChainDefinition{ID, Description, TokenLimit int64, Tasks}` | Same fields | Use as-is |
| `TaskDefinition{ID, Description, Handler, SystemInstruction, ExecuteConfig, RetryOnFailure int, InputVar, Transition}` | Same | Use as-is |
| `LLMExecutionConfig{Model, Provider, Think, Temperature, Tools, HideTools, ToolsPolicies, PassClientsTools, MaxTokensTemplate, Shift, RetryPolicy}` | Same, plus `Models`, `Providers`, `ModelRole`, `MaxTokens` | Use the subset the emitter sets |
| `TaskTransition{OnFailure, Branches}` / `TransitionBranch{Operator, When, Goto, Edge}` | Same | Use as-is |
| `HandleChatCompletion`, `HandleExecuteToolCalls` | Same | Use as-is |
| `OpEdgeTraversedAtLeast`, `OpEquals`, `OpDefault`, `TermEnd` | Same | Use as-is |
| `MaxTokensTemplate` (no JSON tag, set after decode) | Present, `json:"-"` | Use as-is |
| `RetryPolicy *llmretry.RetryPolicy` | Same type path (`runtime/taskengine/llmretry`) | Use as-is |

Notes for the port:

- `emit.go` imports `internal/kernel/taskengine`; the port rewrites that to `github.com/contenox/runtime/runtime/taskengine`.
- The emitter's loop macros are **compile-time**: `{{rounds_used}}` becomes `{{edge_count:<id>-agent-><id>-tools}}`, `{{main_rounds}}` becomes a literal. Those runtime macros already exist, so no engine work.
- `{{var:cwd}}` in the preseed prompts resolves now that `chainTemplateVars` seeds `cwd` from the ACP session (done in Part A).
- `systemInstruction` appends `{{tools}}`; the current `macroenv` renders it.

## 3. Policy format

Current `runtime/runtime/hitlservice/policy.go`: `Policy{DefaultAction, Rules, Compute, Attention}`, `Rule{Tools, Tool, When, Action, TimeoutS, OnTimeout}`, `Condition{Key, Op, Value}`, `ComputeBounds{MaxTurns, MaxToolCalls, MaxTokens, ModelAllowlist, BackendAllowlist, OnExhausted}`, `AttentionBounds{AllowAgentAnswers, MaxAgentAnswers, AllowAgentApprovals, MaxAgentApprovals}`. Operators: `eq`, `glob`, `host`, `command_blacklist`, `command_ask_always`, `no_command_substitution`.

Enterprise adds, and the shipped envelopes depend on:

| Gap | Evidence | Decision |
|---|---|---|
| `Policy.Version int` with `PolicySchemaVersion = 1`, absent = 1, unknown version refused and the run falls back to the fail-closed default | `enterprise .../hitlservice/policy.go` (`PolicySchemaVersion`, `ErrPolicyVersion`) | **Add.** One field, one load check; generated policies stamp `version = 1`. |
| `Policy.TrustedBinaries *TrustedBinaries` — `dirs` + `hashes`, gating every `allow` a `command_prefix_allowlist` grants, refusing path substitution, hash mismatch and unresolvable names | same file; `policy_trusted_binaries_test.go`; `agents.toml` `[envelopes.*.trusted_binaries]` | **Add.** Security feature of the format; without it the envelopes lose a gate they declare. Port `CheckTrustedBinaries` and the evaluator hook, or refuse to load a policy that declares it (a visible "not carried", never silent). |
| `command_prefix_allowlist` operator: whole-line, token-prefix match, refuses redirects and metacharacters | same file; `envelopes.default.shell.prefix_allowlist` | **Add.** The default envelope's shell tiers are meaningless without it. |
| Envelope `extends`, `default_action` sugar (`shell = "approve"`), `deny_paths`/`approve_paths`, `blacklist`/`ask_always`, `deny_hosts`, `timeout`/`on_timeout` | `envelope.go` (`AxisGrant`, `Envelope`, axes `files.read`, `files.write`, `shell`, `network.read`, `network.write`, `missions.fire`, `missions.answer`) | Compile into the current `Rule`/`Condition` vocabulary. No runtime change beyond the two additions above. |
| `WaitIndefinite` timeout (negative duration) | `envelope.go` comment | Map to `TimeoutS = 0` (no deadline) and document it; current semantics already treat 0 as "no rule deadline". |

Rule ordering that must survive the port: unconditional denies first, then the `[policy]` standing rules, then the envelope's own deny/allow, then the posture grant as floor, with `default_action` last. `policy.always_deny` is the root that nothing overrides.

## 4. Tools

Declaration tool names are mapped through `[tools]` in `agents.toml`; the shipped default maps the Claude Code vocabulary, e.g. `Read → local_fs.read_file`, `Bash → local_shell.local_shell` (asserted in `agentdecl_test.go`). Toolset names come from `ToolSets`, which strips the leaf: `local_fs.read_file → local_fs`.

Current providers (`runtime/runtime`): `local_fs`, `local_shell`, `webtools`, `mission`, `oracle`, `shell_session`, MCP servers by registered name, and remote/OpenAPI toolsets by registered name.

Consequences:

- The ACP preseed's `Read`, `Bash` and `"*"` map cleanly.
- Names the current tree does **not** have — the plan mentions `native-git` and `goja`, and any MCP dotted name that is not a registered server — must surface as `Unmapped{reason: "no such toolset"}` on the agent row and in `agent list`, never as a silent drop. A declaration that asks for a missing tool must not compile to a chain that quietly lacks it.
- `Tools.Inherit` (`"*"`) maps to `["*"]`, which the engine already treats as every registered toolset.

## 5. Macros used by the shipped material

| Macro | Status |
|---|---|
| `{{date}}` | Exists |
| `{{host:os}}`, `{{host:arch}}` | Exist |
| `{{tools}}` | Exists (`macroenv` renders the allowed set) |
| `{{var:cwd\|.}}` | Resolves: `chainTemplateVars` seeds `cwd` from the ACP session; `.` is the fallback |
| `{{rounds_used}}`, `{{recovery_rounds_used}}`, `{{main_rounds}}`, `{{recovery_rounds}}` | Emitter-time expansion into `edge_count` macros and literals; no runtime macro needed |

No new macro is required by the preseed. If a user declaration uses an unknown `{{...}}`, the compiler reports it rather than emitting a prompt with a dangling token.

## 6. Registry, sync and generated files

- `Root` is a small abstraction over `vfs.Files` (privileged view) with a stable `Key` for sync state. The current tree has `vfs.OpenPrivilegedView`, so the port keeps the shape and swaps the import.
- `Sync` compiles every source directory into `.generated/` and retires chains whose source vanished. Generated filenames keep the `agent-` prefix so `chainagents.Discover` still recognises them — decide whether to keep that discovery path or replace it (see §8).
- `agents` table already carries what a compiled declaration needs: `kind='chain'`, `config_json.path`, `source`, `registry_id`, `registry_version`, plus `enabled`. A compiled declaration becomes a `kind=chain` row; compiling is what writes it, not a manual step.

## 7. Preseed

`preseed.go` writes 19 shipped Markdown files under the runtime's data directory. The update rule: a file whose bytes equal the shipped version, a known previously-shipped hash, or a recorded hash this mechanism wrote is refreshed; anything else is the user's and is reported as `Edited` and never overwritten. `preseed_blessed.go` is the hash table.

Port this with the same rule and keep `.generated` out of the user's editable tree, so "edit the source, not the output" stays true.

## 8. Search roots

| Enterprise | Current runtime mapping |
|---|---|
| `<project>/.contenox/agents/` | `<project>/.contenox/agents/` (project root is the ACP session cwd) |
| `~/.contenox/agents/` | the runtime home the desktop app already uses (`homedir.Dir()`, i.e. `%APPDATA%`-adjacent `.contenox-harness`), `agents/` subdirectory |
| `.claude/agents/`, `.agents/agents/` | same project/home locations, names scoped by dialect (`claude-code-<name>`) |
| generated output | `<runtime home>/.generated/` and `<project>/.contenox/.generated/` |

`chainagents.Discover(ctx, registry, roots...)` is already called with explicit roots from `acpsvc` and the server API; those calls become "sync declarations, then discover", or the sync result feeds the registry directly. Legacy `agent-*.json` files keep working and are reported as `kind=chain, source=legacy`.

## 9. Port plan

Ordered, each step shippable on its own. Nothing is committed; the owner reviews.

1. **Dependency and skeleton.** Add `github.com/pelletier/go-toml/v2`; copy `agentdecl` into `runtime/runtime/agentdecl` with imports rewritten; strip hosted-only code (`vfs` remote roots, tenancy); gofmt everything the rewrite touched. Gate: package builds, ported unit tests pass.
2. **hitlservice additions.** `Version` + load-time version check, `TrustedBinaries` + `command_prefix_allowlist` (evaluator, vet/diagnostic text, tests ported from enterprise). Gate: existing policies load unchanged; new tests for each addition; a policy declaring an unknown version fails closed.
3. **Envelope compiler and `agents.toml`.** Port `config.go` layering (shipped < user < project < `[agents.<name>]` < declaration < `policy.always_deny`), `envelope.go`, `envelope_emit.go`, `envelope_sync.go`. Ship `agents.toml` with the seven envelopes and `title`/`description`. Gate: each envelope compiles to a policy the current evaluator accepts; golden tests.
   Shell rules must name the shell the host runs: POSIX entries on Linux/macOS, and PowerShell equivalents (`Remove-Item`, `Set-Content`, `Invoke-Expression`, …) plus the Windows credential quarantine (`%APPDATA%`, `%LOCALAPPDATA%`, PSReadLine) on Windows.
4. **Declaration compiler and preseed.** Port `ir.go`, dialects, `tree.go`, `emit.go`, `preseed*.go`, `sync.go`. Add the two UI fields (`category`, `icon`). Gate: compiling the `acp` tree yields a chain equal to the current `chain-acp.json` on the fields that matter (task ids, tools, budgets, transitions) — a golden test that fails on drift.
5. **Switch the default chain.** Ship the `acp` declaration tree, preseed it, and make discovery prefer the compiled chain. Migrate `hitl-policy-{acp,acpx,default,dev,oracle,strict}.json` names onto envelopes; keep a hand-written file shadowing a generated one. Gate: fresh install runs the same chat behaviour; old policy names still resolve.
6. **Agent API and config option.** `_contenox/agents/{list,get,fork,save,setEnabled,remove}`, `_contenox/envelopes/list`, `_contenox/agents/changed`, and the `agent` session config option (fixed after `session/new`). Gate: acpsvc tests per method, including the refusal after session start.
7. **CLI.** `agent list/show/check/edit/enable/disable/remove/fork`, reporting `not carried` and compile errors.
8. **Agents view** (Part D) on top of step 6.

Acceptance for the whole port: a fresh profile has the shipped agents and envelopes with no raw policy JSON in the user's tree; editing a shipped declaration forks it; a broken declaration shows its error instead of disappearing; `agent-*.json` users keep working.

## 10. Risks and open questions

- **Policy feature gap is the real decision.** If `trusted_binaries` or `command_prefix_allowlist` are not added, the envelopes must compile with those blocks reported as not carried — a weaker permission model than the source material, and `default` would lose its shell tiers' safe-prefix gate. Recommendation: add both.
- **TOML dependency** is new to this module; `gopkg.in/yaml.v3` is already there. No alternative — `agents.toml` is the source format.
- **`version` in generated policies** must be stamped so a future format change can refuse older or newer files instead of misreading them.
- **Golden equivalence** with `chain-acp.json` is the safety net for step 5; define the compared fields before writing the compiler, not after.
- **Project-level agents** editable from the app (plan Q4) and automatic reading of other products' folders (Q5) still need the owner's answer.
