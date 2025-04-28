package vectors_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/js402/cate/core/serverops/vectors"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func SetupLocalInstance(ctx context.Context) (string, testcontainers.Container, func(), error) {
	cleanup := func() {}
	exposedPort := "8081/tcp"
	// Create a new container with the specified image and configuration
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:    "../../../",
				Dockerfile: "Dockerfile.vald",
			},
			ExposedPorts:    []string{exposedPort},
			WaitingFor:      wait.ForListeningPort(wait.ForExposedPort().Port).WithStartupTimeout(1 * time.Second),
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

func TestLocalInstance(t *testing.T) {
	_, _, cleanup, err := SetupLocalInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
}

func TestVectors(t *testing.T) {
	uri, _, cleanup, err := SetupLocalInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	client, clean, err := vectors.New(context.Background(), uri)
	if err != nil {
		t.Fatal(err)
	}
	defer clean()

	ctx := context.Background()

	v := vectors.Vector{
		ID:   "test-id",
		Data: []float32{0.1, 0.2, 0.3},
	}

	// Insert
	if err := client.Insert(ctx, v); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Get
	got, err := client.Get(ctx, v.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.ID != v.ID {
		t.Errorf("expected ID %s, got %s", v.ID, got.ID)
	}

	// Search
	results, err := client.Search(ctx, v.Data, 1)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) == 0 {
		t.Errorf("expected search results, got none")
	}

	// Delete
	if err := client.Delete(ctx, v.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}
