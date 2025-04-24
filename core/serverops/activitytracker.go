package serverops

type ActivityTracker interface {
	Start(operation string, args ...any) (report func(err error), reportChange func(subject string), end func())
}

type NoopTracker struct{}

func (NoopTracker) Start(operation string, args ...any) (func(err error), func(subject string), func()) {
	return func(err error) {}, func(subject string) {}, func() {}
}
