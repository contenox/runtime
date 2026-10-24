package libkeypool

import (
	"fmt"
	"hash/fnv"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

type Policy string

const (
	PolicyStickyClient Policy = "sticky_client"
	PolicyRoundRobin   Policy = "round_robin"
)

func Policies() []Policy {
	return []Policy{PolicyStickyClient, PolicyRoundRobin}
}

func ValidPolicy(name Policy) bool {
	return name == "" || slices.Contains(Policies(), name)
}

type KeyPool struct {
	keys    []string
	policy  Policy
	counter uint64
	breaker *ProviderCircuitBreaker
}

func NewKeyPool(keys []string, policy Policy, breaker *ProviderCircuitBreaker) (*KeyPool, error) {
	if !ValidPolicy(policy) {
		names := make([]string, 0, len(Policies()))
		for _, known := range Policies() {
			names = append(names, string(known))
		}
		return nil, fmt.Errorf("keypool: rotation policy %q is not one of %s", policy, strings.Join(names, ", "))
	}
	cleaned := make([]string, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		cleaned = append(cleaned, key)
	}
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("keypool: pool for policy %q holds no key", policy)
	}
	return &KeyPool{keys: cleaned, policy: policy, breaker: breaker}, nil
}

func (p *KeyPool) Keys() []string {
	if p == nil {
		return nil
	}
	return slices.Clone(p.keys)
}

func (p *KeyPool) Policy() Policy {
	if p == nil {
		return ""
	}
	return p.policy
}

func (p *KeyPool) SelectKey(identity string, now time.Time) string {
	if p == nil || len(p.keys) == 0 {
		return ""
	}
	if len(p.keys) == 1 {
		return p.keys[0]
	}

	healthy := make([]string, 0, len(p.keys))
	for _, key := range p.keys {
		if p.breaker == nil || p.breaker.AllowAt(key, now) {
			healthy = append(healthy, key)
		}
	}
	if len(healthy) == 0 {
		healthy = p.keys
	}

	if p.policy != PolicyStickyClient || identity == "" {
		idx := atomic.AddUint64(&p.counter, 1) % uint64(len(healthy))
		return healthy[idx]
	}

	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(identity))
	return healthy[hasher.Sum32()%uint32(len(healthy))]
}
