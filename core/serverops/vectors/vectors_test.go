package vectors_test

import (
	"context"
	"fmt"
	"io"
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
			WaitingFor:      wait.ForListeningPort(wait.ForExposedPort().Port).WithStartupTimeout(10 * time.Second),
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
	uri, container, cleanup, err := SetupLocalInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	// Pass search parameters via Args
	client, clean, err := vectors.New(context.Background(), uri, vectors.Args{
		Timeout: 5 * time.Second,
		Epsilon: 0.1,
		Radius:  -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer clean()

	ctx := context.Background()

	// Create a 784-dimensional vector
	data := make([]float32, 784)
	for i := range data {
		data[i] = 0.1
	}

	v := vectors.Vector{
		ID:   "test-id",
		Data: data,
	}

	// Insert
	if err := client.Insert(ctx, v); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	// time.Sleep(time.Minute)
	var results []vectors.VectorSearchResult
	var searchErr error

	for range 5 {
		results, searchErr = client.Search(ctx, v.Data, 10, 0)
		if searchErr == nil && len(results) > 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	readCloaser, err := container.Logs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer readCloaser.Close()
	logs := make([]byte, 100000)
	_, err = readCloaser.Read(logs)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(fmt.Sprintf("%s", logs))
	// Inside TestVectors, after the loop and before the final check:
	if searchErr != nil || len(results) == 0 {
		readCloser, logErr := container.Logs(ctx)
		if logErr != nil {
			t.Logf("Failed to get container logs: %v", logErr)
		} else {
			defer readCloser.Close()
			// Consider using io.ReadAll for potentially large logs
			logBytes, readErr := io.ReadAll(readCloser)
			if readErr != nil {
				t.Logf("Failed to read container logs: %v", readErr)
			} else {
				t.Logf("Vald Container Logs:\n%s", string(logBytes))
			}
		}
		// Now fail the test
		if searchErr != nil {
			t.Fatalf("Search failed after retries: %v", searchErr)
		} else {
			t.Fatalf("Search returned no results after retries")
		}
	}

	// Delete
	if err := client.Delete(ctx, v.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}
