package libacp

import "strings"

func FlattenContent(blocks []ContentBlock) (text string, dropped []string) {
	var b strings.Builder
	seen := map[string]bool{}

	drop := func(kind string) {
		if !seen[kind] {
			seen[kind] = true
			dropped = append(dropped, kind)
		}
	}
	appendText := func(s string) {
		if s == "" {
			return
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(s)
	}

	for _, block := range blocks {
		switch block.Type {
		case string(ContentKindText):
			appendText(block.Text)
		case string(ContentKindResource):
			if block.Resource != nil && block.Resource.Text != "" {
				appendText(block.Resource.Text)
			} else {
				drop(block.Type)
			}
		case string(ContentKindResourceLink):
			name := strings.TrimSpace(block.Name)
			uri := strings.TrimSpace(block.URI)
			switch {
			case name != "" && uri != "":
				appendText(name + ": " + uri)
			case uri != "":
				appendText(uri)
			case name != "":
				appendText(name)
			default:
				drop(block.Type)
			}
		default:
			drop(block.Type)
		}
	}
	return b.String(), dropped
}
