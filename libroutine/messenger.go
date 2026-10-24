package libroutine

import (
	"context"

	"github.com/contenox/runtime/libbus"
)

func (r *Runner) SubscribeMessenger(ctx context.Context, bus libbus.Messenger, subject string) (libbus.Subscription, error) {
	ch := make(chan []byte, 1)
	sub, err := bus.Stream(ctx, subject, ch)
	if err != nil {
		return nil, err
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-ch:
				if !ok {
					return
				}
				r.Trigger(ctx)
			}
		}
	}()

	return sub, nil
}
