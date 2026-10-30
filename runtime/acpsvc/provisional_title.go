package acpsvc

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	libacp "github.com/contenox/runtime/libacp"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const provisionalTitleKVPrefix = "acp:session_title:"

const provisionalTitleMax = 60

// provisionalTitle derives a title from the user's prompt: the first non-empty
// line (the command name for slash commands), shortened on a word boundary.
func provisionalTitle(input string) string {
	line := ""
	for _, l := range strings.Split(input, "\n") {
		if s := strings.TrimSpace(l); s != "" {
			line = s
			break
		}
	}
	if line == "" {
		return ""
	}
	if strings.HasPrefix(line, "/") {
		return strings.Fields(line)[0]
	}
	if utf8.RuneCountInString(line) <= provisionalTitleMax {
		return line
	}
	runes := []rune(line)
	cut := string(runes[:provisionalTitleMax-1])
	if i := strings.LastIndexAny(cut, " \t"); i > provisionalTitleMax/3 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " \t.,;:") + "…"
}

func readProvisionalTitle(ctx context.Context, exec libdb.Exec, internalSessionID string) string {
	if exec == nil || internalSessionID == "" {
		return ""
	}
	var title string
	if err := runtimetypes.New(exec).GetKV(ctx, provisionalTitleKVPrefix+internalSessionID, &title); err != nil {
		return ""
	}
	return strings.TrimSpace(title)
}

// setProvisionalTitle names an untitled session from its first prompt before
// the chain runs, so a failing first turn still leaves a readable title.
func (t *Transport) setProvisionalTitle(ctx context.Context, sid libacp.SessionID, sess *sessionEntry, input string) {
	if t.deps.DB == nil || sess == nil || sess.InternalSessionID == "" {
		return
	}
	title := provisionalTitle(input)
	if title == "" || t.sessionInfoTitle(ctx, sess.InternalSessionID) != "" {
		return
	}
	data, err := json.Marshal(title)
	if err != nil {
		return
	}
	if err := runtimetypes.New(t.deps.DB.WithoutTransaction()).SetKV(ctx, provisionalTitleKVPrefix+sess.InternalSessionID, data); err != nil {
		return
	}
	t.sendUpdate(ctx, libacp.SessionNotification{
		SessionID: sid,
		Update: libacp.SessionUpdate{
			SessionUpdate: libacp.SessionUpdateSessionInfo,
			Title:         title,
			UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		},
	})
}
