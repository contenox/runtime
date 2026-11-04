package agentdecl_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/stretchr/testify/require"
)

func shippedText(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("agents.toml")
	require.NoError(t, err)
	return string(data)
}

func jsonValue(t *testing.T, text string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	require.NoError(t, dec.Decode(&v))
	return v
}

func commentLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func TestUnit_ConfigEdit_ReplacingAnEnvelopeKeepsEveryOtherLineAndItsOwnLeadComment(t *testing.T) {
	raw := shippedText(t)
	out, err := agentdecl.SetLayerItem(raw, "envelopes", "strict", jsonValue(t, `{
		"extends": "ask_always",
		"description": "Changed by the API.",
		"default_action": "deny",
		"shell": {"grant": "approve", "blacklist": ["mkfs"]},
		"compute": {"max_tool_calls": 77}
	}`))
	require.NoError(t, err)

	require.Contains(t, out, "# The deliberate step up from default, and the one to name when a session should",
		"the comment above the envelope stays")
	require.Contains(t, out, "# The floor. Every surface that does not name an envelope lands here",
		"comments elsewhere stay")
	require.Contains(t, out, "max_tool_calls = 77", "a JSON integer is written as an integer")
	require.NotContains(t, out, "'77'")
	require.NotContains(t, out, "Refuse what is not named; ask about everything that is.")

	cfg, err := agentdecl.ConfigFromTexts(out)
	require.NoError(t, err)
	require.Equal(t, "Changed by the API.", cfg.Envelopes["strict"]["description"])
	require.Equal(t, "read_only", cfg.Envelopes["acpx"]["extends"], "other envelopes are untouched")
	require.Empty(t, agentdecl.ConfigProblems(out))

	lost := 0
	kept := map[string]bool{}
	for _, c := range commentLines(out) {
		kept[c] = true
	}
	for _, c := range commentLines(raw) {
		if !kept[c] {
			lost++
		}
	}
	require.Less(t, lost, 12, "only the comments inside the replaced envelope may go; %d comment lines vanished", lost)
}

func TestUnit_ConfigEdit_DeleteRemovesTheItemItsSubtablesAndItsComment(t *testing.T) {
	raw := shippedText(t)
	out, removed, err := agentdecl.DeleteLayerItem(raw, "envelopes", "acpx")
	require.NoError(t, err)
	require.True(t, removed)
	require.NotContains(t, out, "[envelopes.acpx")
	require.NotContains(t, out, "The envelope for a driver you did not write")
	require.Contains(t, out, "[envelopes.oracle]", "the next envelope stays")
	require.Contains(t, out, "[envelopes.strict]")
	require.NotContains(t, out, "\n\n\n", "no run of blank lines is left behind")

	again, removed, err := agentdecl.DeleteLayerItem(out, "envelopes", "acpx")
	require.NoError(t, err)
	require.False(t, removed)
	require.Equal(t, out, again)
}

func TestUnit_ConfigEdit_CreateAppendsAfterABlankLine(t *testing.T) {
	raw := shippedText(t)
	out, err := agentdecl.SetLayerItem(raw, "envelopes", "mine", jsonValue(t, `{"extends":"read_only","description":"Mine."}`))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(out, raw), "everything that was there is still there, first")
	require.Contains(t, out, "\n\n[envelopes.mine]\n")
	require.NotContains(t, out, "\n[envelopes]\n", "no empty parent table is written")

	empty, err := agentdecl.SetLayerItem("", "envelopes", "mine", jsonValue(t, `{"extends":"read_only"}`))
	require.NoError(t, err)
	require.Equal(t, "[envelopes.mine]\nextends = 'read_only'\n", empty)
}

func TestUnit_ConfigEdit_FlatSectionsEditOneLine(t *testing.T) {
	raw := shippedText(t)
	out, err := agentdecl.SetLayerItem(raw, "tools", "Bash", "local_shell.other")
	require.NoError(t, err)
	require.Contains(t, out, `Bash = "local_shell.other"`)
	require.Equal(t, strings.Count(raw, "\n"), strings.Count(out, "\n"), "one line replaced by one line")

	out, err = agentdecl.SetLayerItem(out, "tools", "WebSearch", "tavily.search")
	require.NoError(t, err)
	require.Contains(t, out, `WebSearch = "tavily.search"`)
	cfg, err := agentdecl.ConfigFromTexts(out)
	require.NoError(t, err)
	require.Equal(t, "tavily.search", cfg.Tools["WebSearch"])
	require.Equal(t, "local_fs.read_file", cfg.Tools["Read"], "neighbours are untouched")

	out, removed, err := agentdecl.DeleteLayerItem(out, "tools", "WebSearch")
	require.NoError(t, err)
	require.True(t, removed)
	require.NotContains(t, out, "\nWebSearch = ", "the entry is gone; the comment that mentions it as an example is not an entry")

	_, err = agentdecl.SetLayerItem(raw, "tools", "Bash", map[string]any{"no": "objects"})
	require.ErrorContains(t, err, "must be a text value")
	_, err = agentdecl.SetLayerItem(raw, "tools", "Bash", "two\nlines")
	require.ErrorContains(t, err, "control characters")
}

func TestUnit_ConfigEdit_SingletonSectionsReplaceAsAWhole(t *testing.T) {
	raw := shippedText(t)
	out, err := agentdecl.SetLayerSection(raw, "chain", jsonValue(t, `{"main_rounds": 20, "retry_policy": {"max_attempts": 2}}`))
	require.NoError(t, err)
	cfg, err := agentdecl.ConfigFromTexts(out)
	require.NoError(t, err)
	require.Equal(t, 20, cfg.Chain.MainRounds)
	require.NotNil(t, cfg.Chain.RetryPolicy)
	require.Equal(t, 2, cfg.Chain.RetryPolicy.MaxAttempts)
	require.Contains(t, out, "# _allowed_commands is enforced per program on a whole command line", "the file header above [chain] stays")
	require.Contains(t, out, "version = 1")

	out, removed, err := agentdecl.DeleteLayerSection(out, "chain")
	require.NoError(t, err)
	require.True(t, removed)
	require.NotContains(t, out, "\n[chain", "the table is gone; the header comment that mentions [chain] stays")

	_, err = agentdecl.SetLayerSection(raw, "envelopes", map[string]any{})
	require.ErrorContains(t, err, "not a single settings section")
}

func TestUnit_ConfigEdit_NamesAreChecked(t *testing.T) {
	for _, bad := range []string{"", "has space", "dot.ted", "-lead", strings.Repeat("a", 65), "new\nline"} {
		_, err := agentdecl.SetLayerItem("", "envelopes", bad, map[string]any{})
		require.Errorf(t, err, "%q", bad)
	}
	_, err := agentdecl.SetLayerItem("", "nothing", "x", map[string]any{})
	require.Error(t, err)
	_, err = agentdecl.SetLayerItem("", "envelopes", "x", nil)
	require.Error(t, err)
	_, err = agentdecl.SetLayerItem("", "envelopes", "x", map[string]any{"v": nil})
	require.ErrorContains(t, err, "null has no TOML form")
}

func TestUnit_ConfigEdit_ArraysOfArraysAndMultilineStringsAreNotTableHeaders(t *testing.T) {
	raw := strings.Join([]string{
		"[agents.one]",
		"pairs = [",
		"  [\"a\", \"b\"],",
		"  [\"c\"],",
		"]",
		"note = \"\"\"",
		"[agents.fake]",
		"\"\"\"",
		"",
		"[agents.two]",
		"x = 1",
		"",
	}, "\n")
	out, err := agentdecl.SetLayerItem(raw, "agents", "two", jsonValue(t, `{"x": 2}`))
	require.NoError(t, err)
	require.Contains(t, out, "[agents.one]\npairs = [\n  [\"a\", \"b\"],")
	require.Contains(t, out, "[agents.fake]\n\"\"\"", "the line inside the string is not treated as a table")
	require.Contains(t, out, "x = 2")

	out, removed, err := agentdecl.DeleteLayerItem(raw, "agents", "one")
	require.NoError(t, err)
	require.True(t, removed)
	require.NotContains(t, out, "pairs")
	require.Contains(t, out, "[agents.two]")
}

func TestUnit_ConfigEdit_ReplacingAGroupSpreadOverSeveralPlacesMergesThem(t *testing.T) {
	raw := "[envelopes.a]\nextends = \"read_only\"\n\n[tools]\nBash = \"x\"\n\n[envelopes.a.shell]\ngrant = \"deny\"\n"
	out, err := agentdecl.SetLayerItem(raw, "envelopes", "a", jsonValue(t, `{"extends":"read_only","description":"merged"}`))
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(out, "[envelopes.a"))
	require.NotContains(t, out, "grant")
	require.Contains(t, out, "[tools]\nBash = \"x\"")
}

func TestUnit_ConfigEdit_AnEditNeverChangesMoreThanItsTargetOnTheShippedFile(t *testing.T) {
	raw := shippedText(t)
	for _, name := range []string{"read_only", "ask_always", "auto_edit", "default", "strict", "acp", "dev", "acpx", "oracle"} {
		out, removed, err := agentdecl.DeleteLayerItem(raw, "envelopes", name)
		require.NoErrorf(t, err, "delete %s", name)
		require.True(t, removed)
		again, err := agentdecl.SetLayerItem(out, "envelopes", name, jsonValue(t, `{"description":"back"}`))
		require.NoErrorf(t, err, "recreate %s", name)
		require.Contains(t, again, "[envelopes."+name+"]")
	}
	for _, section := range []string{"chain", "routing", "naming", "policy"} {
		value, found, err := agentdecl.LayerSection(raw, section)
		require.NoError(t, err)
		require.True(t, found, section)
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		out, err := agentdecl.SetLayerSection(raw, section, jsonValue(t, string(encoded)))
		require.NoErrorf(t, err, "round trip %s", section)
		cfgBefore, err := agentdecl.ConfigFromTexts(raw)
		require.NoError(t, err)
		cfgAfter, err := agentdecl.ConfigFromTexts(out)
		require.NoError(t, err)
		require.Equalf(t, cfgBefore, cfgAfter, "writing %s back unchanged changes nothing", section)
	}
}

func TestUnit_ConfigProblems_OnlyWhatAnEditBreaksIsReported(t *testing.T) {
	good := shippedText(t)
	require.Empty(t, agentdecl.ConfigProblems(good))

	broken, err := agentdecl.SetLayerItem(good, "envelopes", "bad", jsonValue(t, `{"extends":"does_not_exist"}`))
	require.NoError(t, err, "the editor writes what it is given; checking is a separate step")
	problems := agentdecl.ConfigProblems(broken)
	require.NotEmpty(t, problems)
	require.Contains(t, strings.Join(problems, "\n"), "bad")
	require.Equal(t, problems, agentdecl.NewConfigProblems(nil, problems))
	require.Empty(t, agentdecl.NewConfigProblems(problems, problems), "a file that was already broken does not block an unrelated edit")

	unknownKey, err := agentdecl.SetLayerItem(good, "envelopes", "odd", jsonValue(t, `{"nonsense_key": true}`))
	require.NoError(t, err)
	require.NotEmpty(t, agentdecl.ConfigProblems(unknownKey))

	require.NotEmpty(t, agentdecl.ConfigProblems("[envelopes.x\nbroken"), "text that is not TOML is a problem, not a crash")
}

func TestUnit_ConfigLayer_ReadWriteAreAtomicAndRevisioned(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".contenox", "agents.toml")

	layer, err := agentdecl.ReadConfigLayer(path)
	require.NoError(t, err)
	require.False(t, layer.Exists)
	require.Equal(t, agentdecl.LayerRevision(""), layer.Revision)

	require.NoError(t, agentdecl.WriteConfigLayer(path, "[tools]\nBash = \"x\"\n"))
	layer, err = agentdecl.ReadConfigLayer(path)
	require.NoError(t, err)
	require.True(t, layer.Exists)
	require.Equal(t, "[tools]\nBash = \"x\"\n", layer.Raw)
	require.NotEqual(t, agentdecl.LayerRevision(""), layer.Revision)

	require.NoError(t, agentdecl.WriteConfigLayer(path, "[tools]\nBash = \"y\"\n"))
	updated, err := agentdecl.ReadConfigLayer(path)
	require.NoError(t, err)
	require.NotEqual(t, layer.Revision, updated.Revision)

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temp file is left beside it")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.False(t, bytes.Contains(data, []byte("\r")))
}

func TestUnit_MergePatch_FollowsRFC7386(t *testing.T) {
	cases := []struct{ target, patch, want string }{
		{`{"a":"b"}`, `{"a":"c"}`, `{"a":"c"}`},
		{`{"a":"b"}`, `{"b":"c"}`, `{"a":"b","b":"c"}`},
		{`{"a":"b"}`, `{"a":null}`, `{}`},
		{`{"a":"b","b":"c"}`, `{"a":null}`, `{"b":"c"}`},
		{`{"a":["b"]}`, `{"a":"c"}`, `{"a":"c"}`},
		{`{"a":"c"}`, `{"a":["b"]}`, `{"a":["b"]}`},
		{`{"a":{"b":"c"}}`, `{"a":{"b":"d","c":null}}`, `{"a":{"b":"d"}}`},
		{`{"a":[{"b":"c"}]}`, `{"a":[1]}`, `{"a":[1]}`},
		{`{"e":null}`, `{"a":1}`, `{"e":null,"a":1}`},
		{`[1,2]`, `{"a":"b","c":null}`, `{"a":"b"}`},
		{`{}`, `{"a":{"bb":{"ccc":null}}}`, `{"a":{"bb":{}}}`},
	}
	for _, tc := range cases {
		got := agentdecl.MergePatch(jsonValue(t, tc.target), jsonValue(t, tc.patch))
		encoded, err := json.Marshal(got)
		require.NoError(t, err)
		require.JSONEq(t, tc.want, string(encoded), "%s patched with %s", tc.target, tc.patch)
	}
	require.Equal(t, json.Number("3"), agentdecl.MergePatch(jsonValue(t, `{"a":1}`), jsonValue(t, `3`)))
}

func TestUnit_ConfigEdit_PatchChangesOnlyWhatItNamesInsideAnItem(t *testing.T) {
	raw := shippedText(t)
	out, err := agentdecl.PatchLayerItem(raw, "envelopes", "strict", jsonValue(t, `{
		"description": "Patched.",
		"shell": {"blacklist": ["mkfs"]},
		"compute": {"max_tool_calls": 9},
		"network": null
	}`))
	require.NoError(t, err)
	cfg, err := agentdecl.ConfigFromTexts(out)
	require.NoError(t, err)
	strict := cfg.Envelopes["strict"]
	require.Equal(t, "Patched.", strict["description"])
	require.Equal(t, "ask_always", strict["extends"], "what the patch did not name is kept")
	require.Equal(t, "deny", strict["default_action"])
	shell := strict["shell"].(map[string]any)
	require.Equal(t, "approve", shell["grant"], "a nested object is merged, not replaced")
	require.Equal(t, []any{"mkfs"}, shell["blacklist"], "an array is replaced")
	require.EqualValues(t, 9, strict["compute"].(map[string]any)["max_tool_calls"])
	require.EqualValues(t, 1000000, strict["compute"].(map[string]any)["max_tokens"])
	layerItem, inLayer, err := agentdecl.LayerItem(out, "envelopes", "strict")
	require.NoError(t, err)
	require.True(t, inLayer)
	require.NotContains(t, layerItem, "network", "null removes the key from the file; a shipped default underneath is not the file's to remove")
	require.Empty(t, agentdecl.ConfigProblems(out))

	_, err = agentdecl.PatchLayerItem(raw, "envelopes", "ghost", jsonValue(t, `{"description":"x"}`))
	require.ErrorContains(t, err, "is not in this file")
	_, err = agentdecl.PatchLayerItem(raw, "envelopes", "strict", jsonValue(t, `"text"`))
	require.ErrorContains(t, err, "must be an object")

	flat, err := agentdecl.PatchLayerItem(raw, "tools", "Bash", "local_shell.patched")
	require.NoError(t, err)
	require.Contains(t, flat, `Bash = "local_shell.patched"`)
}

func TestUnit_ConfigEdit_PatchOfASingletonSectionCreatesItWhenAbsent(t *testing.T) {
	out, err := agentdecl.PatchLayerSection("", "chain", jsonValue(t, `{"main_rounds": 4}`))
	require.NoError(t, err)
	cfg, err := agentdecl.ConfigFromTexts(out)
	require.NoError(t, err)
	require.Equal(t, 4, cfg.Chain.MainRounds)
	require.Equal(t, 8, cfg.Chain.RecoveryRounds, "everything else stays at the shipped value")

	again, err := agentdecl.PatchLayerSection(out, "chain", jsonValue(t, `{"recovery_rounds": 3}`))
	require.NoError(t, err)
	cfg, err = agentdecl.ConfigFromTexts(again)
	require.NoError(t, err)
	require.Equal(t, 4, cfg.Chain.MainRounds, "a second patch keeps the first")
	require.Equal(t, 3, cfg.Chain.RecoveryRounds)

	_, err = agentdecl.PatchLayerSection("", "envelopes", jsonValue(t, `{}`))
	require.ErrorContains(t, err, "not a single settings section")
}
