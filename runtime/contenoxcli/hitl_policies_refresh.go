package contenoxcli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// hitlPolicyStateFilename records the shipped hash of each preset we last
// managed. A preset whose on-disk bytes still match that hash was not edited by
// the operator, so a new release may replace it; anything else is kept.
const hitlPolicyStateFilename = ".hitl-policies-state.json"

// blessedHITLPolicyHashes are the exact bytes shipped by previous releases.
// They cover installs that predate the state file: untouched shipped content is
// recognised by content, not by a record we did not write yet.
var blessedHITLPolicyHashes = map[string][]string{
	"hitl-policy-default.json": {
		"931f0d41dea98ac0eb409cb678995c1141d62ccc6ddb63f4c5adb86a2d4fd807",
		"570b2756482bcc4ad662bdc446424f53d79b620ab2abd22f4192390858274271",
	},
	"hitl-policy-strict.json": {
		"47bfe6e48dc5e7c9d378384b40519fa3d22c83561ec5d6e04264da04727c74f7",
		"7d862938e1176dffce0ac0664a8db687991db7caa730db232a740dbd298ba46f",
	},
	"hitl-policy-dev.json": {
		"881bed7301c7d52ca1dd8d5845c113876aab0d173dde4ea8004c9fdfbd8da6cc",
		"57d491b25f0e95caf9e1df9af05d975874ec5c75b87b7f1d0bffb9d0bfbfa33c",
	},
	"hitl-policy-acp.json": {
		"48dafb0bc4fbf81ebc05a0c9ca2762d64ad3bc72be5f1952719bb668c0095b2c",
		"692c7cd0c07fd69159861aed97699bc88c7e5ef3e1373cc5f8e87a0c962f328b",
	},
	"hitl-policy-acpx.json": {
		"a28e4a24edeea6f8cbeec31159c4ce4924d0c929139588e5a3867c7072394ef4",
		"3244c466a3f1bf56795dbd8b416cbc28da9140bb7c17fb1673797e600d04766a",
	},
	"hitl-policy-oracle.json": {
		"398fb292aa73f5577b5c0c73ae5bb44284d21483666017126173d50693b30d56",
	},
}

func blessedHITLPolicyHash(name, hash string) bool {
	for _, blessed := range blessedHITLPolicyHashes[name] {
		if blessed == hash {
			return true
		}
	}
	return false
}

type hitlPolicyState struct {
	Hashes map[string]string `json:"hashes"`
}

func digestPolicy(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func readHITLPolicyState(path string) hitlPolicyState {
	state := hitlPolicyState{Hashes: map[string]string{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return state
	}
	_ = json.Unmarshal(raw, &state)
	if state.Hashes == nil {
		state.Hashes = map[string]string{}
	}
	return state
}

// refreshEmbeddedHITLPolicies writes the presets that are missing and refreshes
// copies that are still the untouched shipped content. A preset the operator
// edited is kept. It returns the names it wrote.
func refreshEmbeddedHITLPolicies(dir string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	statePath := filepath.Join(dir, hitlPolicyStateFilename)
	state := readHITLPolicyState(statePath)
	var written []string
	for _, preset := range HITLPolicyPresets {
		shipped := digestPolicy([]byte(preset.Content))
		path := filepath.Join(dir, preset.Name)
		onDisk, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.WriteFile(path, []byte(preset.Content), 0o644); err != nil {
				return written, err
			}
			written = append(written, preset.Name)
		case err != nil:
			return written, err
		case digestPolicy(onDisk) == shipped:
			// Already current.
		case state.Hashes[preset.Name] == digestPolicy(onDisk):
			// Untouched since we shipped it: refresh.
			if err := os.WriteFile(path, []byte(preset.Content), 0o644); err != nil {
				return written, err
			}
			written = append(written, preset.Name)
		case blessedHITLPolicyHash(preset.Name, digestPolicy(onDisk)):
			// Untouched shipped content from a release that predates the state.
			if err := os.WriteFile(path, []byte(preset.Content), 0o644); err != nil {
				return written, err
			}
			written = append(written, preset.Name)
		}
		// Record what we would have shipped regardless: an operator's edit then
		// keeps differing from this hash on every later run and stays untouched.
		state.Hashes[preset.Name] = shipped
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return written, err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(statePath, raw, 0o644); err != nil {
		return written, err
	}
	return written, nil
}
