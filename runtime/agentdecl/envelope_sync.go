package agentdecl

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/contenox/runtime/runtime/hitlservice"
)

func RenderEnvelopePolicy(cfg Config, name, source string) ([]byte, error) {
	env, err := cfg.ResolveEnvelope(name)
	if err != nil {
		return nil, err
	}
	out, err := TranspileEnvelope(env)
	if err != nil {
		return nil, err
	}
	if source == "" {
		source = ConfigFilename
	}
	origin := fmt.Sprintf("Rendered from [%s.%s] in %s. Derived and disposable: edit the envelope, not this file. A hand-written %s in .contenox/ or ~/.contenox/ shadows it.",
		EnvelopeSection, name, source, EnvelopePolicyFile(name))
	if env.Description != "" {
		origin = env.Description + " " + origin
	}
	annotations := [][2]any{{"//", origin}}
	if len(out.Notes) > 0 {
		annotations = append(annotations, [2]any{"//reserved", out.Notes})
	}
	return marshalPolicyDoc(out.Policy, annotations)
}

func SyncEnvelopePolicy(ctx context.Context, cfg Config, name string, generatedDir Root, source string) (string, bool, error) {
	if generatedDir.FS == nil {
		return "", false, fmt.Errorf("agentdecl: generated root %q holds no handle", generatedDir.Key)
	}
	raw, err := RenderEnvelopePolicy(cfg, name, source)
	if err != nil {
		return "", false, err
	}
	path := generatedDir.Path(EnvelopePolicyFile(name))
	if fileHas(ctx, generatedDir.FS, EnvelopePolicyFile(name), raw) {
		return path, false, nil
	}
	if err := generatedDir.FS.MkdirAll(ctx, "."); err != nil {
		return "", false, fmt.Errorf("agentdecl: create %s: %w", generatedDir.Key, err)
	}
	if err := generatedDir.FS.WriteFile(ctx, EnvelopePolicyFile(name), raw); err != nil {
		return "", false, fmt.Errorf("agentdecl: write %s: %w", path, err)
	}
	return path, true, nil
}

func marshalPolicyDoc(p *hitlservice.Policy, annotations [][2]any) ([]byte, error) {
	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	head := fmt.Sprintf("{\n  \"$schema\": %q,\n", PolicySchemaURL)
	for _, ann := range annotations {
		value, err := json.Marshal(ann[1])
		if err != nil {
			return nil, err
		}
		head += fmt.Sprintf("  %q: %s,\n", ann[0], value)
	}
	if len(body) < 2 || body[0] != '{' || body[1] != '\n' {
		return body, nil
	}
	return append([]byte(head), body[2:]...), nil
}
