package libtracker

import "context"

type ActivityTracker interface {
	Start(
		ctx context.Context,
		operation string,
		subject string,
		kvArgs ...any,
	) (
		reportErr func(err error),
		reportChange func(id string, data any),
		end func(),
	)
}

type NoopTracker struct{}

func (NoopTracker) Start(
	ctx context.Context,
	operation string,
	subject string,
	kvArgs ...any,
) (
	func(error),
	func(string, any),
	func(),
) {
	return func(error) {}, func(string, any) {}, func() {}
}

var _ ActivityTracker = NoopTracker{}
