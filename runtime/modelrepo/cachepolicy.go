package modelrepo

import "time"

type ModelCachePolicy struct {
	Healthy time.Duration
	Empty   time.Duration
	Error   time.Duration
}

type CachePolicyCatalog interface {
	CachePolicy() ModelCachePolicy
}
