package libtestenv

import (
	"context"
	"fmt"

	"github.com/testcontainers/testcontainers-go"
)

type WorkerConfig struct {
	APIBaseURL                  string
	WorkerEmail                 string
	WorkerPassword              string
	WorkerLeaserID              string
	WorkerLeaseDurationSeconds  int
	WorkerRequestTimeoutSeconds int
	WorkerType                  string
}

func SetupLocalWorkerInstance(ctx context.Context, config WorkerConfig) (testcontainers.Container, func(), error) {
	quiet()()
	// Create a new container with the specified image and configuration
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:    "../../",
				Dockerfile: "Dockerfile.worker",
			},
			Env: map[string]string{
				"API_BASE_URL":                   config.APIBaseURL,
				"WORKER_TYPE":                    config.WorkerType,
				"WORKER_EMAIL":                   config.WorkerEmail,
				"WORKER_PASSWORD":                config.WorkerPassword,
				"WORKER_LEASER_ID":               config.WorkerLeaserID,
				"WORKER_LEASE_DURATION_SECONDS":  fmt.Sprintf("%d", config.WorkerLeaseDurationSeconds),
				"WORKER_REQUEST_TIMEOUT_SECONDS": fmt.Sprintf("%d", config.WorkerRequestTimeoutSeconds),
			},
		},
		Started: true,
	})
	if err != nil {
		return nil, func() {}, err
	}

	return container, func() {
		container.Terminate(ctx)
	}, nil
}
