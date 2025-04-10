package libbus

import (
	"context"
	"log"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
)

func SetupNatsInstance(ctx context.Context) (string, testcontainers.Container, func(), error) {
	cleanup := func() {}
	natsContainer, err := nats.Run(ctx, "nats:2.10")
	if err != nil {
		return "", nil, cleanup, err
	}
	cleanup = func() {
		if err := testcontainers.TerminateContainer(natsContainer); err != nil {
			log.Printf("failed to terminate container: %s", err)
		}
	}
	cons, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		return "", nil, cleanup, err
	}
	return cons, natsContainer, cleanup, nil
}
