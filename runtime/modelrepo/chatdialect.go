package modelrepo

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/contenox/runtime/runtime/reasoning"
)

var ErrUnknownChatDialect = errors.New("unknown chat dialect")

type UnsupportedDialectLevelError struct {
	Dialect string
	Level   string
}

func (e *UnsupportedDialectLevelError) Error() string {
	return fmt.Sprintf("chat dialect %s has no published thinking level %q", e.Dialect, e.Level)
}

type WireMessage map[string]any

type ChatDialect interface {
	Name() string
	DocURL() string
	RewriteBody(body map[string]any, req DialectRequest) error
	OwnsReasoning() bool
	ThinkingMember(req DialectRequest) string
	WireAssistantMessage(content, thinking string) (WireMessage, error)
}

type DialectRequest struct {
	Think      *string
	Tools      bool
	CacheHints *CacheHints
	Thinking   *ThinkingTypes
}

func (r DialectRequest) ThinkLevel() (string, error) {
	if r.Think == nil {
		return "", nil
	}
	level, ok, err := reasoning.NormalizeOptional(*r.Think)
	if err != nil {
		return "", err
	}
	if !ok || level == reasoning.Auto {
		return "", nil
	}
	return level, nil
}

func (r DialectRequest) AllowsEffort(wire string) bool {
	if r.Thinking == nil || len(r.Thinking.EffortLevels) == 0 {
		return true
	}
	return r.Thinking.SupportsEffort(wire)
}

var (
	chatDialectMu       sync.RWMutex
	chatDialectRegistry = map[string]ChatDialect{}
)

func RegisterChatDialect(dialect ChatDialect) {
	if dialect == nil {
		panic("modelrepo: chat dialect cannot be nil")
	}
	name := strings.ToLower(strings.TrimSpace(dialect.Name()))
	if name == "" {
		panic("modelrepo: chat dialect name cannot be empty")
	}
	chatDialectMu.Lock()
	defer chatDialectMu.Unlock()
	if _, exists := chatDialectRegistry[name]; exists {
		panic("modelrepo: chat dialect already registered for " + name)
	}
	chatDialectRegistry[name] = dialect
}

func ChatDialectFor(name string) (ChatDialect, error) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return nil, nil
	}
	chatDialectMu.RLock()
	defer chatDialectMu.RUnlock()
	dialect, ok := chatDialectRegistry[normalized]
	if !ok {
		return nil, fmt.Errorf("%w %q (registered: %s)", ErrUnknownChatDialect, name, strings.Join(ChatDialectNames(), ", "))
	}
	return dialect, nil
}

func MustChatDialectFor(name string) ChatDialect {
	dialect, err := ChatDialectFor(name)
	if err != nil {
		panic(err)
	}
	return dialect
}

func ChatDialectNames() []string {
	chatDialectMu.RLock()
	defer chatDialectMu.RUnlock()
	names := make([]string, 0, len(chatDialectRegistry))
	for name := range chatDialectRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func RewriteChatBody(dialect ChatDialect, body []byte, req DialectRequest) ([]byte, error) {
	if dialect == nil {
		return body, nil
	}
	var members map[string]any
	if err := json.Unmarshal(body, &members); err != nil {
		return nil, fmt.Errorf("chat dialect %s: decode request body: %w", dialect.Name(), err)
	}
	if err := dialect.RewriteBody(members, req); err != nil {
		return nil, fmt.Errorf("chat dialect %s: %w", dialect.Name(), err)
	}
	rewritten, err := json.Marshal(members)
	if err != nil {
		return nil, fmt.Errorf("chat dialect %s: encode request body: %w", dialect.Name(), err)
	}
	return rewritten, nil
}
