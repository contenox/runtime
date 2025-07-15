package libkv

import (
	"context"
	"time"

	"github.com/valkey-io/valkey-go"
)

type VKManager struct {
	client valkey.Client
	ttl    time.Duration
}

type Config struct {
	Addr     string
	Password string
}

func NewManager(cfg Config, ttl time.Duration) (*VKManager, error) {
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress: []string{cfg.Addr},
		Password:    cfg.Password,
	})
	if err != nil {
		return nil, err
	}
	return &VKManager{
		client: client,
		ttl:    ttl,
	}, nil
}

func (m *VKManager) Operation(ctx context.Context) (KVExec, error) {
	return &VKExec{
		client: m.client,
		ttl:    m.ttl,
	}, nil
}

func (m *VKManager) Close() error {
	m.client.Close()
	return nil
}

type VKExec struct {
	client valkey.Client
	ttl    time.Duration
}

func (r *VKExec) Get(ctx context.Context, key []byte) ([]byte, error) {
	cmd := r.client.B().Get().Key(string(key)).Build()
	res, err := r.client.Do(ctx, cmd).AsBytes()
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return res, nil
}

func (r *VKExec) Set(ctx context.Context, kv KeyValue) error {
	keyStr := string(kv.Key)
	value := kv.Value

	var ttl time.Duration
	switch {
	case !kv.TTL.IsZero():
		ttl = time.Until(kv.TTL)
	case r.ttl > 0:
		ttl = r.ttl
	}

	var cmd valkey.Completed
	switch {
	case ttl > 0:
		ttlMs := max(ttl.Milliseconds(), 1)
		cmd = r.client.B().Set().Key(keyStr).Value(string(value)).PxMilliseconds(ttlMs).Build()
	case ttl < 0:
		cmd = r.client.B().Set().Key(keyStr).Value(string(value)).PxMilliseconds(1).Build()
	default:
		cmd = r.client.B().Set().Key(keyStr).Value(string(value)).Build()
	}

	return r.client.Do(ctx, cmd).Error()
}

func (r *VKExec) Delete(ctx context.Context, key []byte) error {
	cmd := r.client.B().Del().Key(string(key)).Build()
	_, err := r.client.Do(ctx, cmd).AsInt64()
	return err
}

func (r *VKExec) Exists(ctx context.Context, key []byte) (bool, error) {
	cmd := r.client.B().Exists().Key(string(key)).Build()
	res, err := r.client.Do(ctx, cmd).AsInt64()
	if err != nil {
		return false, err
	}
	return res > 0, nil
}

func (r *VKExec) List(ctx context.Context) ([]string, error) {
	cmd := r.client.B().Keys().Pattern("*").Build()
	return r.client.Do(ctx, cmd).AsStrSlice()
}

func (r *VKExec) LPush(ctx context.Context, key []byte, value []byte) error {
	cmd := r.client.B().Lpush().Key(string(key)).Element(string(value)).Build()
	return r.client.Do(ctx, cmd).Error()
}

func (r *VKExec) LRange(ctx context.Context, key []byte, start, stop int64) ([][]byte, error) {
	cmd := r.client.B().Lrange().Key(string(key)).Start(start).Stop(stop).Build()
	s, err := r.client.Do(ctx, cmd).AsStrSlice()
	if err != nil {
		return nil, err
	}
	bSlice := make([][]byte, len(s))
	for i, v := range s {
		bSlice[i] = []byte(v)
	}
	return bSlice, nil
}

func (r *VKExec) RPop(ctx context.Context, key []byte) ([]byte, error) {
	cmd := r.client.B().Rpop().Key(string(key)).Build()
	res, err := r.client.Do(ctx, cmd).AsBytes()
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return res, nil
}

func (r *VKExec) LTrim(ctx context.Context, key []byte, start, stop int64) error {
	cmd := r.client.B().Ltrim().Key(string(key)).Start(start).Stop(stop).Build()
	return r.client.Do(ctx, cmd).Error()
}

func (r *VKExec) LLen(ctx context.Context, key []byte) (int64, error) {
	cmd := r.client.B().Llen().Key(string(key)).Build()
	return r.client.Do(ctx, cmd).AsInt64()
}

func (r *VKExec) SAdd(ctx context.Context, key []byte, value []byte) error {
	cmd := r.client.B().Sadd().Key(string(key)).Member(string(value)).Build()
	return r.client.Do(ctx, cmd).Error()
}

func (r *VKExec) SMembers(ctx context.Context, key []byte) ([][]byte, error) {
	cmd := r.client.B().Smembers().Key(string(key)).Build()
	s, err := r.client.Do(ctx, cmd).AsStrSlice()
	if err != nil {
		return nil, err
	}
	bSlice := make([][]byte, len(s))
	for i, v := range s {
		bSlice[i] = []byte(v)
	}
	return bSlice, nil
}
