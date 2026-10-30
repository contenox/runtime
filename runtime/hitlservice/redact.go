package hitlservice

import "regexp"

const redactedMark = "[redacted]"

// secretPatterns are applied in order. Each replacement keeps the surrounding
// text (flag name, header scheme, variable name) so a summary stays readable
// while the secret value is removed.
var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// URL userinfo: https://user:pass@host
	{regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s:@]*:[^/\s@]+@`), "${1}" + redactedMark + "@"},
	// Authorization header values, with or without a scheme word.
	{regexp.MustCompile(`(?i)(authorization["']?\s*[:=]\s*(?:bearer\s+|basic\s+|token\s+)?)[^\s"']+`), "${1}" + redactedMark},
	// Bare "Bearer xxx" / "Basic xxx" credentials.
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`), "${1} " + redactedMark},
	// Flags whose name says secret: --password=x, --token x, -api-key=x.
	{regexp.MustCompile(`(?i)(--?[\w-]*(?:password|passwd|token|secret|api-?key|credential|auth)[\w-]*)(=|\s+)("[^"]*"|'[^']*'|[^\s"']+)`), "${1}${2}" + redactedMark},
	// Tools that take a password through a short flag: sshpass -p x, docker login -p x, curl -u user:pass.
	{regexp.MustCompile(`(?i)(\b(?:sshpass|docker\s+login|podman\s+login)\b[^|;&\n]*?\s-p)(\s+|=)("[^"]*"|'[^']*'|\S+)`), "${1}${2}" + redactedMark},
	{regexp.MustCompile(`(\bmysql(?:dump|admin)?\b[^|;&\n]*?\s-p)\S+`), "${1}" + redactedMark},
	{regexp.MustCompile(`(\bcurl\b[^|;&\n]*?\s(?:-u|--user)(?:\s+|=))("[^"]*"|'[^']*'|[^\s"']+)`), "${1}" + redactedMark},
	// KEY=value assignments (env prefixes, export, $env:KEY=) where KEY looks secret.
	{regexp.MustCompile(`(?i)\b([A-Za-z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|API_KEY|APIKEY|KEY|CREDENTIAL|AUTH)[A-Za-z0-9_]*)=("[^"]*"|'[^']*'|[^\s"']+)`), "${1}=" + redactedMark},
	// Well-known token shapes, wherever they appear.
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`), redactedMark},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`), redactedMark},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`), redactedMark},
	{regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{16,}`), redactedMark},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), redactedMark},
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), redactedMark},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`), redactedMark},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`), redactedMark},
}

// redactSecrets removes credential values from a string that is about to be
// stored or shown (the approval args summary). It is heuristic: it covers the
// common shapes and leaves ordinary commands readable.
func redactSecrets(s string) string {
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}
