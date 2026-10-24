package libtracker

import "context"

type ChainedTracker []ActivityTracker

func NewChainedTracker(trackers ...ActivityTracker) ActivityTracker {
	return ChainedTracker(trackers)
}

func (ct ChainedTracker) Start(
	ctx context.Context,
	operation string,
	subject string,
	kvArgs ...any,
) (
	reportErr func(err error),
	reportChange func(id string, data any),
	end func(),
) {
	var reportErrs []func(error)
	var reportChanges []func(string, any)
	var ends []func()

	for _, tracker := range ct {
		rerr, rchange, endFn := tracker.Start(ctx, operation, subject, kvArgs...)
		reportErrs = append(reportErrs, rerr)
		reportChanges = append(reportChanges, rchange)
		ends = append(ends, endFn)
	}

	return func(err error) {
			for _, fn := range reportErrs {
				fn(err)
			}
		},
		func(id string, data any) {
			for _, fn := range reportChanges {
				fn(id, data)
			}
		},
		func() {
			for _, fn := range ends {
				fn()
			}
		}
}
