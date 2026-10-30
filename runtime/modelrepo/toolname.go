package modelrepo

import "strings"

// SanitizeToolName maps an engine tool name (for example "local_fs.read_file")
// to the form the provider wire formats accept: ASCII letters, digits, '_' and
// '-', with leading and trailing '_'/'-' trimmed. Provider codecs apply this
// rule (plus provider-specific length limits and de-duplication) when they send
// tools, so a name recorded in a provider continuation is in this space.
func SanitizeToolName(in string) string {
	var b strings.Builder
	for _, r := range in {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_-")
}
