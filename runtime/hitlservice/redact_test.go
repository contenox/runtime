package hitlservice

import (
	"strings"
	"testing"
)

func TestUnit_RedactSecrets_RemovesEachShape(t *testing.T) {
	cases := []struct{ name, in, secret string }{
		{"openai key", "curl x -d sk-abcdefghijklmnopqrstuvwx", "sk-abcdefghijklmnopqrstuvwx"},
		{"github ghp", "git push https://x/y ghp_abcdefghijklmnopqrstuvwxyz0123", "ghp_abcdefghijklmnopqrstuvwxyz0123"},
		{"github gho", "echo gho_abcdefghijklmnopqrstuvwxyz0123", "gho_abcdefghijklmnopqrstuvwxyz0123"},
		{"github pat", "echo github_pat_11ABCDEFG0abcdefghijkl_mnop", "github_pat_11ABCDEFG0abcdefghijkl_mnop"},
		{"slack", "echo xoxb-1234567890-abcdefghij", "xoxb-1234567890-abcdefghij"},
		{"aws", "aws s3 ls AKIAIOSFODNN7EXAMPLE", "AKIAIOSFODNN7EXAMPLE"},
		{"jwt", "echo eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.abcDEF123", "eyJhbGciOiJIUzI1NiJ9"},
		{"auth header bearer", `curl -H "Authorization: Bearer abc123SECRETvalue" https://h`, "abc123SECRETvalue"},
		{"auth header basic", `curl -H 'Authorization: Basic dXNlcjpwYXNz' https://h`, "dXNlcjpwYXNz"},
		{"bare bearer", "curl -H Bearer:x Bearer abcdefgh12345678", "abcdefgh12345678"},
		{"password flag eq", "mytool --password=hunter2 run", "hunter2"},
		{"token flag space", "mytool --token hunter2 run", "hunter2"},
		{"quoted flag", `mytool --password "hunter 2" run`, "hunter 2"},
		{"sshpass -p", "sshpass -p hunter2 ssh host", "hunter2"},
		{"mysql -p attached", "mysql -u root -phunter2 db", "hunter2"},
		{"docker login -p", "docker login -u me -p hunter2", "hunter2"},
		{"curl -u", "curl -u admin:hunter2 https://h", "hunter2"},
		{"env prefix", "API_KEY=hunter2 ./run.sh", "hunter2"},
		{"export secret", "export MY_SECRET='hunter 2' && go test", "hunter 2"},
		{"powershell env", `$env:GITHUB_TOKEN="hunter2"; gh pr list`, "hunter2"},
		{"db password var", "DB_PASSWORD=hunter2 psql", "hunter2"},
		{"url userinfo", "git clone https://user:hunter2@github.com/o/r.git", "hunter2"},
	}
	for _, c := range cases {
		got := redactSecrets(c.in)
		if strings.Contains(got, c.secret) {
			t.Errorf("%s: secret survived: %q", c.name, got)
		}
		if !strings.Contains(got, redactedMark) {
			t.Errorf("%s: no redaction mark: %q", c.name, got)
		}
	}
}

func TestUnit_RedactSecrets_LeavesOrdinaryCommandsReadable(t *testing.T) {
	for _, in := range []string{
		"git push origin main",
		"mkdir -p build/out",
		"cp -p a b",
		"go test ./... -run TestKeyboard",
		"ls -la /home/me/.ssh",
		"curl https://example.com/path?x=1",
		"npm install --save-dev typescript",
		"git clone https://github.com/o/r.git",
		`Get-ChildItem -Path C:\Users\me\src`,
		"echo sk-short",
		"docker run -p 8080:80 nginx",
	} {
		if got := redactSecrets(in); got != in {
			t.Errorf("ordinary command changed: %q -> %q", in, got)
		}
	}
}

func TestUnit_SummarizeApprovalArgs_RedactsBeforeTruncating(t *testing.T) {
	long := strings.Repeat("a", 80) + " API_KEY=hunter2hunter2hunter2"
	got := summarizeApprovalArgs(map[string]any{"command": long})
	if strings.Contains(got, "hunter2") || strings.Contains(got, "API_KEY=h") {
		t.Fatalf("secret leaked through truncation: %q", got)
	}
	got = summarizeApprovalArgs(map[string]any{"command": "curl -u admin:hunter2 https://h"})
	if strings.Contains(got, "hunter2") {
		t.Fatalf("secret leaked: %q", got)
	}
}
