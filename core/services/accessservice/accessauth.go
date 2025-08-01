package accessservice

import (
	"context"
	"time"

	libdb "github.com/contenox/dbexec"
	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
)

type authorizationDecorator struct {
	service Service
	db      libdb.DBManager
}

func (a *authorizationDecorator) Create(ctx context.Context, entry *AccessEntryRequest) (*AccessEntryRequest, error) {
	tx := a.db.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), a, store.PermissionManage); err != nil {
		return nil, err
	}
	return a.service.Create(ctx, entry)
}

func (a *authorizationDecorator) GetByID(ctx context.Context, entry AccessEntryRequest) (*AccessEntryRequest, error) {
	tx := a.db.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), a, store.PermissionView); err != nil {
		return nil, err
	}
	return a.service.GetByID(ctx, entry)
}

func (a *authorizationDecorator) Update(ctx context.Context, entry *AccessEntryRequest) (*AccessEntryRequest, error) {
	tx := a.db.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), a, store.PermissionManage); err != nil {
		return nil, err
	}
	return a.service.Update(ctx, entry)
}

func (a *authorizationDecorator) Delete(ctx context.Context, id string) error {
	tx := a.db.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), a, store.PermissionManage); err != nil {
		return err
	}
	return a.service.Delete(ctx, id)
}

func (a *authorizationDecorator) ListAll(ctx context.Context, starting time.Time, withDetails bool) ([]AccessEntryRequest, error) {
	tx := a.db.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), a, store.PermissionView); err != nil {
		return nil, err
	}
	return a.service.ListAll(ctx, starting, withDetails)
}

func (a *authorizationDecorator) ListByIdentity(ctx context.Context, identity string, withDetails bool) ([]AccessEntryRequest, error) {
	tx := a.db.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), a, store.PermissionView); err != nil {
		return nil, err
	}
	return a.service.ListByIdentity(ctx, identity, withDetails)
}

// Implement ServiceMeta interface by forwarding to wrapped service
func (a *authorizationDecorator) GetServiceName() string {
	return a.service.GetServiceName()
}

func (a *authorizationDecorator) GetServiceGroup() string {
	return a.service.GetServiceGroup()
}

// WithAuthorization decorates the given Service with authorization checks
func WithAuthorization(service Service, db libdb.DBManager) Service {
	return &authorizationDecorator{
		service: service,
		db:      db,
	}
}

var _ Service = (*authorizationDecorator)(nil)
