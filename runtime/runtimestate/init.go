package runtimestate

import (
	"context"
	"errors"
	"fmt"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/google/uuid"
)

type Config struct {
	DatabaseURL string `json:"database_url"`
	TaskModel   string `json:"task_model"`
	ChatModel   string `json:"chat_model"`
	TenantID    string `json:"tenant_id"`
}

type modelCapability int

const (
	canEmbed modelCapability = iota
	canPrompt
	canChat
)

func InitPromptExec(ctx context.Context, config *Config, dbInstance libdb.DBManager, runtime *State, contextLen int) error {
	tx, com, r, err := dbInstance.WithTransaction(ctx)
	if err != nil {
		return err
	}
	defer r()

	group, err := initTaskgroup(ctx, config, tx, false)
	if err != nil {
		return fmt.Errorf("init task group: %w", err)
	}
	model, err := initTaskModel(ctx, config, tx, contextLen)
	if err != nil {
		return fmt.Errorf("init task model: %w", err)
	}
	if err = assignModelTogroup(ctx, config, tx, model, group); err != nil {
		return fmt.Errorf("assign task model to group: %w", err)
	}
	return com(ctx)
}

func InitChatExec(ctx context.Context, config *Config, dbInstance libdb.DBManager, runtime *State, contextLen int) error {
	tx, com, r, err := dbInstance.WithTransaction(ctx)
	if err != nil {
		return err
	}
	defer r()

	group, err := initChatgroup(ctx, config, tx, false)
	if err != nil {
		return fmt.Errorf("init chat group: %w", err)
	}
	model, err := initChatModel(ctx, config, tx, contextLen)
	if err != nil {
		return fmt.Errorf("init chat model: %w", err)
	}
	if err = assignModelTogroup(ctx, config, tx, model, group); err != nil {
		return fmt.Errorf("assign chat model to group: %w", err)
	}
	return com(ctx)
}

func initTaskgroup(ctx context.Context, config *Config, tx libdb.Exec, created bool) (*runtimetypes.AffinityGroup, error) {
	group, err := runtimetypes.New(tx).GetAffinityGroup(ctx, runtimetypes.TasksgroupID)
	if !created && errors.Is(err, libdb.ErrNotFound) {
		err = runtimetypes.New(tx).CreateAffinityGroup(ctx, &runtimetypes.AffinityGroup{
			ID:          runtimetypes.TasksgroupID,
			Name:        runtimetypes.TasksgroupName,
			PurposeType: "Internal Tasks",
		})
		if err != nil {
			return nil, err
		}
		return initTaskgroup(ctx, config, tx, true)
	}
	if err != nil {
		return nil, err
	}
	return group, nil
}

func initChatgroup(ctx context.Context, config *Config, tx libdb.Exec, created bool) (*runtimetypes.AffinityGroup, error) {
	group, err := runtimetypes.New(tx).GetAffinityGroup(ctx, runtimetypes.ChatgroupID)
	if !created && errors.Is(err, libdb.ErrNotFound) {
		err = runtimetypes.New(tx).CreateAffinityGroup(ctx, &runtimetypes.AffinityGroup{
			ID:          runtimetypes.ChatgroupID,
			Name:        runtimetypes.ChatgroupName,
			PurposeType: "Internal Chat",
		})
		if err != nil {
			return nil, err
		}
		return initChatgroup(ctx, config, tx, true)
	}
	if err != nil {
		return nil, err
	}
	return group, nil
}

func initOrUpdateModel(ctx context.Context, tx libdb.Exec, tenantID, modelName string, contextLength int, capability modelCapability) (*runtimetypes.Model, error) {
	if modelName == "" {
		return nil, errors.New("model name cannot be empty")
	}
	parsedTenantID, err := uuid.Parse(tenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid tenant_id: %w", err)
	}
	modelID := uuid.NewSHA1(parsedTenantID, []byte(modelName))
	storeInstance := runtimetypes.New(tx)

	model, err := storeInstance.GetModelByName(ctx, modelName)

	if errors.Is(err, libdb.ErrNotFound) {
		newModel := &runtimetypes.Model{
			Model:         modelName,
			ID:            modelID.String(),
			ContextLength: contextLength,
		}
		switch capability {
		case canEmbed:
			newModel.CanEmbed = true
		case canPrompt:
			newModel.CanPrompt = true
		case canChat:
			newModel.CanChat = true
		}
		if err := storeInstance.AppendModel(ctx, newModel); err != nil {
			return nil, fmt.Errorf("failed to append new model '%s': %w", modelName, err)
		}
		return newModel, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get model '%s': %w", modelName, err)
	}

	needsUpdate := false
	switch capability {
	case canEmbed:
		if !model.CanEmbed {
			model.CanEmbed = true
			needsUpdate = true
		}
	case canPrompt:
		if !model.CanPrompt {
			model.CanPrompt = true
			needsUpdate = true
		}
	case canChat:
		if !model.CanChat {
			model.CanChat = true
			needsUpdate = true
		}
	}

	if needsUpdate {
		if err := storeInstance.UpdateModel(ctx, model); err != nil {
			return nil, fmt.Errorf("failed to update model '%s' capabilities: %w", modelName, err)
		}
	}

	return model, nil
}

func initTaskModel(ctx context.Context, config *Config, tx libdb.Exec, contextLength int) (*runtimetypes.Model, error) {
	return initOrUpdateModel(ctx, tx, config.TenantID, config.TaskModel, contextLength, canPrompt)
}

func initChatModel(ctx context.Context, config *Config, tx libdb.Exec, contextLength int) (*runtimetypes.Model, error) {
	return initOrUpdateModel(ctx, tx, config.TenantID, config.ChatModel, contextLength, canChat)
}

type ExtraModelSpec struct {
	Name          string
	ContextLength int
	CanChat       bool
	CanPrompt     bool
	CanEmbed      bool
}

func EnsureModels(ctx context.Context, dbInstance libdb.DBManager, tenantID string, specs []ExtraModelSpec) error {
	if len(specs) == 0 {
		return nil
	}
	tx, com, r, err := dbInstance.WithTransaction(ctx)
	if err != nil {
		return err
	}
	defer r()
	for _, spec := range specs {
		if spec.Name == "" {
			return fmt.Errorf("extra_models entry has empty name")
		}
		if spec.CanEmbed {
			if _, err := initOrUpdateModel(ctx, tx, tenantID, spec.Name, spec.ContextLength, canEmbed); err != nil {
				return fmt.Errorf("extra model %s: %w", spec.Name, err)
			}
		}
		if spec.CanPrompt {
			if _, err := initOrUpdateModel(ctx, tx, tenantID, spec.Name, spec.ContextLength, canPrompt); err != nil {
				return fmt.Errorf("extra model %s: %w", spec.Name, err)
			}
		}
		if spec.CanChat {
			if _, err := initOrUpdateModel(ctx, tx, tenantID, spec.Name, spec.ContextLength, canChat); err != nil {
				return fmt.Errorf("extra model %s: %w", spec.Name, err)
			}
		}
	}
	return com(ctx)
}

func assignModelTogroup(ctx context.Context, _ *Config, tx libdb.Exec, model *runtimetypes.Model, group *runtimetypes.AffinityGroup) error {
	storeInstance := runtimetypes.New(tx)
	models, err := storeInstance.ListModelsForAffinityGroup(ctx, group.ID)
	if err != nil {
		return err
	}
	for _, presentModel := range models {
		if presentModel.ID == model.ID {
			return nil
		}
	}
	if err := storeInstance.AssignModelToAffinityGroup(ctx, group.ID, model.ID); err != nil {
		return err
	}
	return nil
}
