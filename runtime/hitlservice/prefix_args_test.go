package hitlservice_test

import (
	"testing"

	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/stretchr/testify/assert"
)

func TestUnit_CommandLineAllowedByPrefix_ToleratesPackageManagerGlobalFlags(t *testing.T) {
	cases := []struct {
		name    string
		line    string
		prefix  string
		allowed bool
	}{
		{
			name:    "prefix flag before the verb",
			line:    "npm --prefix packages/harness run build",
			prefix:  "npm run build",
			allowed: true,
		},
		{
			name:    "prefix flag before the verb with test arguments after --",
			line:    "npm --prefix packages/harness test -- src/pages/chat/lib/workspaceRoot.test.ts",
			prefix:  "npm test",
			allowed: true,
		},
		{
			name:    "equals form of the prefix flag",
			line:    "npm --prefix=packages/harness run build",
			prefix:  "npm run build",
			allowed: true,
		},
		{
			name:    "directory flag",
			line:    "pnpm -C packages/ui run lint",
			prefix:  "pnpm run lint",
			allowed: true,
		},
		{
			name:    "separator without global flags",
			line:    "npm test -- src/a.test.ts",
			prefix:  "npm test",
			allowed: true,
		},
		{
			name:    "a different verb is not allowed by the prefix",
			line:    "npm --prefix packages/harness install",
			prefix:  "npm test",
			allowed: false,
		},
		{
			name:    "an argument named like a prefix",
			line:    "npm install --prefix packages/harness",
			prefix:  "npm test",
			allowed: false,
		},
		{
			name:    "flags after the separator are not skipped",
			line:    "npm run --prefix test",
			prefix:  "npm test",
			allowed: false,
		},
		{
			name:    "global flags are only stripped for package managers",
			line:    "node --prefix x test",
			prefix:  "node test",
			allowed: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.allowed, hitlservice.CommandLineAllowedByPrefix(tc.line, tc.prefix))
		})
	}
}
