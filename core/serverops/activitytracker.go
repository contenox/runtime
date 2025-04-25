package serverops

// ActivityTracker defines a hook interface for observing the lifecycle of an operation.
// It provides three functions:
//   - A function to report an error result of the operation.
//   - A function to report any state changes that occurred during the operation.
//   - A function to signal the end of the operation.
//
// This interface is typically used to wrap service calls in order to:
//   - Record metrics
//   - Emit logs
//   - Track side effects or changes to system state
//   - Report to external systems such as tracing backends or activity streams
//
// Callers should invoke Start at the beginning of an operation, passing any relevant arguments.
// The returned functions must be invoked to properly report the operation’s result and side effects.
//
// Example usage:
//
//	reportErr, reportChange, end := tracker.Start("Service.Method", arg1, arg2)
//	defer end()
//	result, err := realService.Method(...)
//	if err != nil {
//	    reportErr(err)
//	} else {
//	    reportChange("some-entity-id")
//	}
type ActivityTracker interface {
	Start(operation string, args ...any) (reportErr func(err error), reportChange func(subject string), end func())
}

type NoopTracker struct{}

func (NoopTracker) Start(operation string, args ...any) (func(err error), func(subject string), func()) {
	return func(err error) {}, func(subject string) {}, func() {}
}
