package libroutine

import (
	"context"
	"log"
	"sync"
	"time"
)

type group struct {
	managers   map[string]*Routine
	loops      map[string]bool
	triggerChs map[string]chan struct{}
	mu         sync.Mutex
}

var (
	groupInstance *group
	groupOnce     sync.Once
)

func GetGroup() *group {
	groupOnce.Do(func() {
		log.Println("Initializing routine group")
		groupInstance = &group{
			managers:   make(map[string]*Routine),
			loops:      make(map[string]bool),
			triggerChs: make(map[string]chan struct{}),
		}
	})
	return groupInstance
}

type LoopConfig struct {
	Key          string
	Threshold    int
	ResetTimeout time.Duration
	Interval     time.Duration
	Operation    func(ctx context.Context) error
}

func (p *group) StartLoop(ctx context.Context, cfg *LoopConfig) {
	p.mu.Lock()
	log.Printf("Starting loop for key: %s", cfg.Key)
	defer p.mu.Unlock()

	manager, exists := p.managers[cfg.Key]
	if !exists {
		log.Printf("Creating new routine manager for key: %s", cfg.Key)
		manager = NewRoutine(cfg.Threshold, cfg.ResetTimeout)
		p.managers[cfg.Key] = manager
	}

	if p.loops[cfg.Key] {
		log.Printf("Loop for key %s is already active", cfg.Key)
		return
	}

	triggerChan := make(chan struct{}, 1)
	p.triggerChs[cfg.Key] = triggerChan

	p.loops[cfg.Key] = true

	go func() {
		log.Printf("Loop started for key: %s", cfg.Key)
		manager.Loop(ctx, cfg.Interval, triggerChan, cfg.Operation, func(err error) {
			if err != nil {
				log.Printf("Error in loop for key %s: %v", cfg.Key, err)
			}
		})
		p.mu.Lock()
		delete(p.loops, cfg.Key)
		delete(p.triggerChs, cfg.Key)
		p.mu.Unlock()
		log.Printf("Loop stopped for key: %s", cfg.Key)
	}()
}

func (p *group) IsLoopActive(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loops[key]
}

func (p *group) ForceUpdate(key string) {
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

func (p *group) GetManager(key string) *Routine {
	p.mu.Lock()
	defer p.mu.Unlock()
	log.Printf("Retrieving manager for key: %s", key)
	return p.managers[key]
}

type Pool struct {
	mu           sync.RWMutex
	routines     map[string]*Routine
	threshold    int
	resetTimeout time.Duration
}

func NewPool(threshold int, resetTimeout time.Duration) *Pool {
	return &Pool{
		routines:     make(map[string]*Routine),
		threshold:    threshold,
		resetTimeout: resetTimeout,
	}
}

func (p *Pool) Get(key string) *Routine {
	if p == nil || key == "" {
		return nil
	}
	p.mu.RLock()
	r, ok := p.routines[key]
	p.mu.RUnlock()
	if ok {
		return r
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if r, ok = p.routines[key]; ok {
		return r
	}
	r = NewRoutine(p.threshold, p.resetTimeout)
	p.routines[key] = r
	return r
}

func (p *Pool) Allow(key string) bool {
	return p.AllowAt(key, time.Now())
}

func (p *Pool) AllowAt(key string, now time.Time) bool {
	if p == nil || key == "" {
		return true
	}
	p.mu.RLock()
	r, ok := p.routines[key]
	p.mu.RUnlock()
	if !ok {
		return true
	}
	return r.AllowAt(now)
}

func (p *Pool) WouldAllowAt(key string, now time.Time) bool {
	if p == nil || key == "" {
		return true
	}
	p.mu.RLock()
	r, ok := p.routines[key]
	p.mu.RUnlock()
	if !ok {
		return true
	}
	return r.WouldAllowAt(now)
}

func (p *Pool) MarkSuccess(key string) {
	if p == nil || key == "" {
		return
	}
	p.mu.RLock()
	r, ok := p.routines[key]
	p.mu.RUnlock()
	if ok {
		r.ForceClose()
	}
}
