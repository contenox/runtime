package accessservice

import (
	"context"
	"time"

	"github.com/js402/CATE/internal/serverops"
	"github.com/js402/CATE/internal/serverops/store"
	"github.com/js402/CATE/libs/libdb"
)

type Service struct {
	dbInstance      libdb.DBManager
	securityEnabled bool
	jwtSecret       string
}

func New(db libdb.DBManager) *Service {
	return &Service{dbInstance: db}
}

func (s *Service) Create(ctx context.Context, entry *store.AccessEntry) error {
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionManage); err != nil {
		return err
	}
	tx := s.dbInstance.WithoutTransaction()
	return store.New(tx).CreateAccessEntry(ctx, entry)
}

func (s *Service) GetByID(ctx context.Context, id string) (*store.AccessEntry, error) {
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionView); err != nil {
		return nil, err
	}
	tx := s.dbInstance.WithoutTransaction()
	entry, err := store.New(tx).GetAccessEntryByID(ctx, id)
	return entry, err
}

func (s *Service) Update(ctx context.Context, entry *store.AccessEntry) error {
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionManage); err != nil {
		return err
	}
	tx := s.dbInstance.WithoutTransaction()
	err := store.New(tx).UpdateAccessEntry(ctx, entry)
	return err
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionManage); err != nil {
		return err
	}
	tx := s.dbInstance.WithoutTransaction()
	err := store.New(tx).DeleteAccessEntry(ctx, id)
	return err
}

func (s *Service) ListAll(ctx context.Context, starting time.Time) ([]*store.AccessEntry, error) {
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionView); err != nil {
		return nil, err
	}
	tx := s.dbInstance.WithoutTransaction()
	return store.New(tx).ListAccessEntries(ctx, starting)
}

func (s *Service) ListByIdentity(ctx context.Context, identity string) ([]*store.AccessEntry, error) {
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionView); err != nil {
		return nil, err
	}
	tx := s.dbInstance.WithoutTransaction()
	return store.New(tx).GetAccessEntriesByIdentity(ctx, identity)
}

type LoginArgs struct {
	Subject    string
	SigningKey []byte
	Password   string
	JWTSecret  string
	JWTExpiry  string
}

func (s *Service) GetServiceName() string {
	return "accessservice"
}

func (s *Service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
