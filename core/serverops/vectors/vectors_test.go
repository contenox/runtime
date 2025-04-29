package vectors_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/js402/cate/core/serverops/vectors"
)

func TestLocalInstance(t *testing.T) {
	_, _, cleanup, err := vectors.SetupLocalInstance(context.Background(), "../../../")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
}

func TestVectors(t *testing.T) {
	uri, _, cleanup, err := vectors.SetupLocalInstance(context.Background(), "../../../")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	client, clean, err := vectors.New(context.Background(), uri, vectors.Args{
		Timeout: 1 * time.Second,
		SearchArgs: vectors.SearchArgs{
			Epsilon: 0.1,
			Radius:  -1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer clean()

	ctx := context.Background()
	t.Run("Empty Search", func(t *testing.T) {
		emptyVec := make([]float32, 784)
		_, err := client.Search(ctx, emptyVec, 1, 1, nil)
		if err == nil {
			t.Error("Expected error for empty vector search")
		}
	})
	t.Run("Basic CRUD Operations", func(t *testing.T) {
		// Test data
		data := make([]float32, 784)
		for i := range data {
			data[i] = float32(i%255) / 255.0
		}
		v := vectors.Vector{
			ID:   "crud-test",
			Data: data,
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
			t.Errorf("Get returned wrong ID: got %s, want %s", got.ID, v.ID)
		}
		time.Sleep(time.Second)
		// Search
		results, err := client.Search(ctx, v.Data, 1, 1, nil)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("Search returned no results")
		}
		if results[0].ID != v.ID {
			t.Errorf("Search returned wrong ID: got %s, want %s", results[0].ID, v.ID)
		}

		// Delete
		if err := client.Delete(ctx, v.ID); err != nil {
			t.Fatalf("Delete failed: %v", err)
		}

		// Verify deletion
		_, err = client.Get(ctx, v.ID)
		if err == nil {
			t.Error("Vector still exists after deletion")
		}
	})

	t.Run("Upsert Operations", func(t *testing.T) {
		v := vectors.Vector{
			ID:   "upsert-test",
			Data: make([]float32, 784),
		}

		// Initial insert
		if err := client.Insert(ctx, v); err != nil {
			t.Fatalf("Initial insert failed: %v", err)
		}

		// Update data
		updatedData := make([]float32, 784)
		for i := range updatedData {
			updatedData[i] = float32(i%100) / 100.0
		}
		updated := vectors.Vector{
			ID:   v.ID,
			Data: updatedData,
		}

		// Upsert
		if err := client.Upsert(ctx, updated); err != nil {
			t.Fatalf("Upsert failed: %v", err)
		}

		// Verify update
		got, err := client.Get(ctx, v.ID)
		if err != nil {
			t.Fatalf("Get after upsert failed: %v", err)
		}
		if !reflect.DeepEqual(got.Data, updatedData) {
			t.Error("Vector data not updated by upsert")
		}
	})

	t.Run("Batch Operations", func(t *testing.T) {
		const batchSize = 10
		vs := make([]vectors.Vector, batchSize)
		for i := range batchSize {
			data := make([]float32, 784)
			for j := range data {
				data[j] = float32(i+j) / 1000.0
			}
			vs[i] = vectors.Vector{
				ID:   fmt.Sprintf("batch-%d", i),
				Data: data,
			}
		}

		// Batch insert
		if err := client.BatchInsert(ctx, vs); err != nil {
			t.Fatalf("Batch insert failed: %v", err)
		}

		// Verify all inserted
		for _, v := range vs {
			_, err := client.Get(ctx, v.ID)
			if err != nil {
				t.Errorf("Get failed for batch vector %s: %v", v.ID, err)
			}
		}

		// Batch cleanup
		for _, v := range vs {
			if err := client.Delete(ctx, v.ID); err != nil {
				t.Errorf("Delete failed for batch vector %s: %v", v.ID, err)
			}
		}
	})

	t.Run("Search Parameters", func(t *testing.T) {
		v := vectors.Vector{
			ID:   "search-params-test",
			Data: make([]float32, 784),
		}

		if err := client.Insert(ctx, v); err != nil {
			t.Fatal(err)
		}
		defer client.Delete(ctx, v.ID)

		// Test different result counts
		t.Run("Multiple Results", func(t *testing.T) {
			time.Sleep(time.Second)
			results, err := client.Search(ctx, v.Data, 3, 1, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) < 1 {
				t.Error("Expected at least 1 result")
			}
		})

		t.Run("Strict Matching", func(t *testing.T) {
			_, err := client.Search(ctx, v.Data, 1, 1, &vectors.SearchArgs{
				Radius:  0.03,
				Epsilon: 0.001,
			})
			if err != nil {
				t.Errorf("Strict search failed: %v", err)
			}
		})
	})

	t.Run("Error Handling", func(t *testing.T) {
		t.Run("Non-existent ID", func(t *testing.T) {
			_, err := client.Get(ctx, "non-existent-id")
			if err == nil {
				t.Error("Expected error for non-existent ID")
			}
		})
	})
}
