package modelrepo

import "sync"

var (
	shutdownHooksMu sync.Mutex
	shutdownHooks   []func() error
)

func RegisterShutdownHook(fn func() error) {
	if fn == nil {
		return
	}
	shutdownHooksMu.Lock()
	defer shutdownHooksMu.Unlock()
	shutdownHooks = append(shutdownHooks, fn)
}

func Shutdown() error {
	shutdownHooksMu.Lock()
	hooks := make([]func() error, len(shutdownHooks))
	copy(hooks, shutdownHooks)
	shutdownHooksMu.Unlock()

	var first error
	for _, fn := range hooks {
		if err := fn(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
