package vectors

import (
	"context"
	"fmt"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func SetupLocalInstance(ctx context.Context, contextLocation string) (string, testcontainers.Container, func(), error) {
	cleanup := func() {}
	exposedPort := "8081/tcp"
	// Create a new container with the specified image and configuration
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:    contextLocation,
				Dockerfile: "Dockerfile.vald",
			},
			ExposedPorts:    []string{exposedPort},
			WaitingFor:      wait.ForListeningPort(wait.ForExposedPort().Port).WithStartupTimeout(20 * time.Second),
			AlwaysPullImage: false,
		},
		Started: true,
	})
	if err != nil {
		return "", nil, cleanup, err
	}
	mappedPort, err := container.MappedPort(ctx, "8081")
	if err != nil {
		return "", nil, cleanup, err
	}
	host, err := container.Host(ctx)
	if err != nil {
		return "", nil, cleanup, err
	}
	uri := fmt.Sprintf("%s:%s", host, mappedPort.Port())
	time.Sleep(time.Second * 10)
	cleanup = func() {
		container.Terminate(ctx)
	}
	return uri, container, cleanup, nil
}
