package modeldconn

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/contenox/runtime/internal/modeldinstall"
	"github.com/contenox/runtime/internal/remedy"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modeldprobe"
)

// InventoryTarget inspects the selected owner on its own host without loading a model.
func InventoryTarget(ctx context.Context, target ModeldTarget) (transport.Inventory, error) {
	client, err := executionEndpoint(ctx, target)
	if err != nil {
		return transport.Inventory{}, err
	}
	return client.Inventory(ctx)
}

// Inventory inspects the installed native runtime without starting an owner or loading a model.
func (m *Workers) Inventory(ctx context.Context) (transport.Inventory, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	detected := modeldprobe.New(m.root).Detect()
	if detected.Binary == "" {
		return transport.Inventory{}, fmt.Errorf("%w; "+remedy.PrepareLocal, modeldprobe.ErrNotInstalled)
	}
	if err := modeldinstall.ValidateBinary(ctx, detected.Binary, ""); err != nil {
		return transport.Inventory{}, fmt.Errorf("update local inference before selecting devices: %w", err)
	}
	command := modeldinstall.CommandContext(ctx, detected.Binary, "inventory", "--json")
	output, err := command.Output()
	if err != nil {
		return transport.Inventory{}, fmt.Errorf("native device inventory failed: %w", err)
	}
	var inventory transport.Inventory
	if err := json.Unmarshal(output, &inventory); err != nil {
		return inventory, fmt.Errorf("native inventory response: %w", err)
	}
	return inventory, nil
}
