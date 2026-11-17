package owner

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/liblease"
)

type workerClaims struct {
	mu       sync.Mutex
	client   *grpc.Client
	epoch    string
	instance string
	claims   map[string]transport.DeviceClaim
}

func (c *workerClaims) ClaimDevices(ctx context.Context, allocation transport.NativeAllocation) error {
	result, err := c.client.ClaimDevices(ctx, transport.DeviceClaimRequest{OwnerInstance: c.epoch, WorkerInstance: c.instance, Devices: allocation.Devices, Config: allocation.Config, Resources: allocation.Resources})
	if err != nil {
		return err
	}
	permit, managed := ctx.Value(computePermitKey{}).(admission.Permit)
	if !managed {
		return errors.New("native allocation has no managed request permit")
	}
	if err := c.client.ResumeAtBoundary(ctx, permit); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, claim := range result.Claims {
		if claim.DeviceID == "" || claim.Instance == "" || claim.LeasePath == "" {
			return errors.New("owner returned an incomplete device claim")
		}
		c.claims[claim.DeviceID] = claim
	}
	for _, device := range allocation.Devices {
		if _, claimed := c.claims[device.ID]; !claimed {
			return errors.New("owner did not claim every resolved accelerator")
		}
	}
	return c.checkLocked(ctx)
}

func (c *workerClaims) check(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.checkLocked(ctx)
}

func (c *workerClaims) checkLocked(ctx context.Context) error {
	for _, claim := range c.claims {
		record, err := liblease.InspectContext(ctx, claim.LeasePath)
		if err != nil {
			return err
		}
		if record.InstanceID != claim.Instance || !time.Now().Add(250*time.Millisecond).Before(record.ExpiresAt()) {
			return transport.ErrStaleFence
		}
	}
	return nil
}
