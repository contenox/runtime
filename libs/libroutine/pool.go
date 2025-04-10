package libroutine

import (
	"context"
	"log"
	"sync"
	"time"
)

// Pool manages routines by key.
type Pool struct {
	managers   map[string]*Routine      // Maps keys to Routine instances
	loops      map[string]bool          // Tracks whether a loop is active for a key
	triggerChs map[string]chan struct{} // Per-key trigger channels for forcing an update
	mu         sync.Mutex               // Protects access to maps
}

var (
	poolInstance *Pool
	poolOnce     sync.Once
)

// GetPool returns the singleton instance of the Pool.
func GetPool() *Pool {
	poolOnce.Do(func() {
		log.Println("Initializing routine pool")
		poolInstance = &Pool{
			managers:   make(map[string]*Routine),
			loops:      make(map[string]bool),
			triggerChs: make(map[string]chan struct{}),
		}
	})
	return poolInstance
}

// StartLoop starts a managed loop for the given key, ensuring only one instance runs.
func (p *Pool) StartLoop(ctx context.Context, key string, threshold int, resetTimeout time.Duration, interval time.Duration, fn func(ctx context.Context) error) {
	p.mu.Lock()
	log.Printf("Starting loop for key: %s", key)
	defer p.mu.Unlock()

	// Create a new Routine if none exists for the key.
	if _, exists := p.managers[key]; !exists {
		log.Printf("Creating new routine manager for key: %s", key)
		p.managers[key] = NewRoutine(threshold, resetTimeout)
	}

	// If a loop for this key is already active, do nothing.
	if p.loops[key] {
		log.Printf("Loop for key %s is already active", key)
		return
	}

	// Create a new trigger channel for this loop.
	triggerChan := make(chan struct{}, 1)
	p.triggerChs[key] = triggerChan

	// Mark the loop as active.
	p.loops[key] = true

	// Start the loop in a new goroutine.
	go func() {
		log.Printf("Loop started for key: %s", key)
		p.managers[key].Loop(ctx, interval, triggerChan, fn, func(err error) {
			if err != nil {
				log.Printf("Error in loop for key %s: %v", key, err)
			}
		})
		// Clean up when the loop exits.
		p.mu.Lock()
		delete(p.loops, key)
		delete(p.triggerChs, key)
		p.mu.Unlock()
		log.Printf("Loop stopped for key: %s", key)
	}()
}

// IsLoopActive is an accessor for tests to check if a loop is active for a key.
func (p *Pool) IsLoopActive(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loops[key]
}

func (p *Pool) ForceUpdate(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	log.Printf("Forcing update for key: %s", key)
	if triggerChan, ok := p.triggerChs[key]; ok {
		select {
		case triggerChan <- struct{}{}:
			log.Printf("Update triggered for key: %s", key)
		default:
			log.Printf("Update already pending for key: %s", key)
		}
	}
}

// GetManager exposes the Routine associated with a key for testing.
func (p *Pool) GetManager(key string) *Routine {
	p.mu.Lock()
	defer p.mu.Unlock()
	log.Printf("Retrieving manager for key: %s", key)
	return p.managers[key]
}
