package llamasession

import (
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/llama"
)

type cacheTestModel struct {
	closed atomic.Int32
}

func (m *cacheTestModel) Close() {
	m.closed.Add(1)
}

func TestUnit_EmbedCache_ConcurrentAcquisitionLoadsOnce(t *testing.T) {
	var cache embedModelCache[*cacheTestModel]
	var loads atomic.Int32
	model := &cacheTestModel{}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			em, release, err := cache.acquire("model.gguf", llama.Config{NumGpuLayers: 4}, func() (*cacheTestModel, error) {
				loads.Add(1)
				return model, nil
			})
			if err != nil {
				t.Error(err)
				return
			}
			if em.model != model || model.closed.Load() != 0 {
				t.Error("acquisition returned wrong or closed model")
			}
			release()
			release()
		}()
	}
	close(start)
	wg.Wait()
	if loads.Load() != 1 {
		t.Fatalf("model loads = %d, want 1", loads.Load())
	}
	if err := cache.close(); err != nil {
		t.Fatal(err)
	}
	if model.closed.Load() != 1 {
		t.Fatalf("model closes = %d, want 1", model.closed.Load())
	}
}

func TestUnit_EmbedCache_ConfigAndPathIdentifyModel(t *testing.T) {
	var cache embedModelCache[*cacheTestModel]
	var models []*cacheTestModel
	load := func() (*cacheTestModel, error) {
		model := &cacheTestModel{}
		models = append(models, model)
		return model, nil
	}
	for _, cfg := range []llama.Config{
		{NumGpuLayers: 2, TensorSplit: []float32{1, 0}},
		{NumGpuLayers: 3, TensorSplit: []float32{1, 0}},
		{NumGpuLayers: 2, TensorSplit: []float32{0, 1}},
		{NumGpuLayers: 2, TensorSplit: []float32{1, 0}},
	} {
		_, release, err := cache.acquire("model.gguf", cfg, load)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	_, release, err := cache.acquire("other.gguf", llama.Config{NumGpuLayers: 2, TensorSplit: []float32{1, 0}}, load)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if len(models) != 4 {
		t.Fatalf("model loads = %d, want 4 distinct path/config identities", len(models))
	}
	if err := cache.close(); err != nil {
		t.Fatal(err)
	}
	if len(cache.models) != 0 {
		t.Fatal("release retained cache entries")
	}
	if err := cache.close(); err != nil {
		t.Fatal(err)
	}
	for _, model := range models {
		if model.closed.Load() != 1 {
			t.Fatalf("model closes = %d, want 1", model.closed.Load())
		}
	}
	_, release, err = cache.acquire("model.gguf", llama.Config{NumGpuLayers: 2, TensorSplit: []float32{1, 0}}, load)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if len(models) != 5 {
		t.Fatal("acquisition after release reused a closed model")
	}
	if err := cache.close(); err != nil {
		t.Fatal(err)
	}
}

func TestUnit_EmbedCache_CloseWaitsForActiveEmbedding(t *testing.T) {
	var cache embedModelCache[*cacheTestModel]
	model := &cacheTestModel{}
	_, release, err := cache.acquire("model.gguf", llama.Config{}, func() (*cacheTestModel, error) { return model, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	started := make(chan struct{})
	closed := make(chan error, 1)
	go func() {
		close(started)
		closed <- cache.close()
	}()
	<-started
	select {
	case err := <-closed:
		t.Fatalf("cache close returned while model active: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if model.closed.Load() != 0 {
		t.Fatal("active embedding model was closed")
	}
	release()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cache close did not finish after embedding released")
	}
	if model.closed.Load() != 1 {
		t.Fatalf("model closes = %d, want 1", model.closed.Load())
	}
}

func TestUnit_EmbedCache_ReportsLoadAndConfigFailures(t *testing.T) {
	var cache embedModelCache[*cacheTestModel]
	want := errors.New("load failed")
	if _, _, err := cache.acquire("model.gguf", llama.Config{}, func() (*cacheTestModel, error) { return nil, want }); !errors.Is(err, want) {
		t.Fatalf("load error = %v, want %v", err, want)
	}
	called := false
	if _, _, err := cache.acquire("model.gguf", llama.Config{TensorSplit: []float32{float32(math.NaN())}}, func() (*cacheTestModel, error) {
		called = true
		return &cacheTestModel{}, nil
	}); err == nil || called {
		t.Fatalf("invalid config should fail before load: called=%v error=%v", called, err)
	}
	if err := cache.close(); err != nil {
		t.Fatal(err)
	}
	_, release, err := cache.acquire("model.gguf", llama.Config{}, func() (*cacheTestModel, error) { return &cacheTestModel{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := cache.close(); err != nil {
		t.Fatal(err)
	}
}
