package accessservice

import (
	"context"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/libs/libdb"
	"github.com/google/uuid"
)

type service struct {
	dbInstance      libdb.DBManager
	securityEnabled bool
	jwtSecret       string
}

func New(db libdb.DBManager) Service {
	return &service{dbInstance: db}
}

type Service interface {
	serverops.ServiceMeta
	Create(ctx context.Context, entry *AccessEntryRequest) (*AccessEntryRequest, error)
	GetByID(ctx context.Context, entry AccessEntryRequest) (*AccessEntryRequest, error)
	Update(ctx context.Context, entry *AccessEntryRequest) (*AccessEntryRequest, error)
	Delete(ctx context.Context, id string) error
	ListAll(ctx context.Context, starting time.Time, withDetails bool) ([]AccessEntryRequest, error)
	ListByIdentity(ctx context.Context, identity string, withDetails bool) ([]AccessEntryRequest, error)
}

type AccessEntryRequest struct {
	ID           string `json:"id"`
	Identity     string `json:"identity"`
	Resource     string `json:"resource"`
	ResourceType string `json:"resourceType"`
	Permission   string `json:"permission"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	WithUserDetails *bool         `json:"withUserDetails,omitempty"`
	IdentityDetails *UserMetadata `json:"identityDetails,omitempty"`
	FileDetails     *FileMetadata `json:"fileDetails,omitempty"`
}

type FileMetadata struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Type string `json:"type"`
}

type UserMetadata struct {
	ID           string `json:"id"`
	FriendlyName string `json:"friendlyName"`
	Email        string `json:"email"`
	Subject      string `json:"subject"`
}

func (s *service) Create(ctx context.Context, entry *AccessEntryRequest) (*AccessEntryRequest, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return nil, err
	}
	perm, err := store.PermissionFromString(entry.Permission)
	if err != nil {
		return nil, err
	}
	if entry.ResourceType == "" {
		return nil, serverops.ErrMissingParameter
	}
	if entry.ResourceType == store.ResourceTypeFiles {
		_, err = store.New(tx).GetFileByID(ctx, entry.Resource)
		if err != nil {
			return nil, err
		}
	}
	id := uuid.NewString()
	err = store.New(tx).CreateAccessEntry(ctx, &store.AccessEntry{
		ID:           id,
		Identity:     entry.Identity,
		Permission:   perm,
		Resource:     entry.Resource,
		ResourceType: entry.ResourceType,
	})
	if err != nil {
		return nil, err
	}
	withDetails := false
	if entry.WithUserDetails != nil && *entry.WithUserDetails {
		withDetails = true
	}
	return s.getByID(ctx, tx, id, withDetails)
}

func (s *service) GetByID(ctx context.Context, entry AccessEntryRequest) (*AccessEntryRequest, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	withDetails := false
	if entry.WithUserDetails != nil && *entry.WithUserDetails {
		withDetails = true
	}
	return s.getByID(ctx, tx, entry.ID, withDetails)
}

func (s *service) getByID(ctx context.Context, tx libdb.Exec, id string, withUser bool) (*AccessEntryRequest, error) {
	entry, err := store.New(tx).GetAccessEntryByID(ctx, id)
	if err != nil {
		return nil, err
	}
	res := &AccessEntryRequest{
		Identity:     entry.Identity,
		Resource:     entry.Resource,
		ResourceType: entry.ResourceType,
		Permission:   entry.Permission.String(),
		UpdatedAt:    entry.UpdatedAt,
		CreatedAt:    entry.CreatedAt,
	}
	if withUser {
		user, err := store.New(tx).GetUserBySubject(ctx, entry.Identity)
		if err != nil {
			return nil, err
		}
		res.IdentityDetails = &UserMetadata{
			ID:           user.ID,
			Subject:      user.Subject,
			Email:        user.Email,
			FriendlyName: user.FriendlyName,
		}
	}
	return res, err
}

func (s *service) Update(ctx context.Context, entry *AccessEntryRequest) (*AccessEntryRequest, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return nil, err
	}
	perm, err := store.PermissionFromString(entry.Permission)
	if err != nil {
		return nil, err
	}
	if entry.ResourceType == "" {
		return nil, serverops.ErrMissingParameter
	}
	if entry.ResourceType == store.ResourceTypeFiles {
		_, err = store.New(tx).GetFileByID(ctx, entry.Resource)
		if err != nil {
			return nil, err
		}
	}
	err = store.New(tx).UpdateAccessEntry(ctx, &store.AccessEntry{
		ID:           entry.ID,
		Identity:     entry.Identity,
		ResourceType: entry.ResourceType,
		Permission:   perm,
		Resource:     entry.Resource,
	})
	withDetails := false
	if entry.WithUserDetails != nil && *entry.WithUserDetails {
		withDetails = true
	}
	return s.getByID(ctx, tx, entry.ID, withDetails)
}

func (s *service) Delete(ctx context.Context, id string) error {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	err := store.New(tx).DeleteAccessEntry(ctx, id)
	return err
}

func (s *service) ListAll(ctx context.Context, starting time.Time, withDetails bool) ([]AccessEntryRequest, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	entries, err := store.New(tx).ListAccessEntries(ctx, starting)
	if err != nil {
		return nil, err
	}
	cE := make([]AccessEntryRequest, len(entries))
	subjects := make([]string, len(entries))
	for i := range entries {
		cE[i] = AccessEntryRequest{
			ID:           entries[i].ID,
			Identity:     entries[i].Identity,
			ResourceType: entries[i].ResourceType,
			Permission:   entries[i].Permission.String(),
			Resource:     entries[i].Resource,
		}
		subjects[i] = entries[i].Identity
		if withDetails && entries[i].ResourceType == store.ResourceTypeFiles {
			file, err := store.New(tx).GetFileByID(ctx, entries[i].Resource)
			if err != nil {
				return nil, err
			}
			cE[i].FileDetails = &FileMetadata{
				ID: file.ID,
				// Path: file.Path,
				Type: file.Type,
			}
		}
	}
	if withDetails {
		users, err := store.New(tx).ListUsersBySubjects(ctx, subjects...)
		if err != nil {
			return nil, err
		}
		for i, u := range users {
			cE[i].IdentityDetails = &UserMetadata{
				ID:           u.ID,
				Subject:      u.Subject,
				Email:        u.Email,
				FriendlyName: u.FriendlyName,
			}
		}
	}
	return cE, err
}

func (s *service) ListByIdentity(ctx context.Context, identity string, withDetails bool) ([]AccessEntryRequest, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	entries, err := store.New(tx).GetAccessEntriesByIdentity(ctx, identity)
	if err != nil {
		return nil, err
	}
	cE := make([]AccessEntryRequest, len(entries))
	subjects := make([]string, len(entries))
	for i := range entries {
		cE[i] = AccessEntryRequest{
			ID:           entries[i].ID,
			Identity:     entries[i].Identity,
			Permission:   entries[i].Permission.String(),
			Resource:     entries[i].Resource,
			ResourceType: entries[i].ResourceType,
		}
		subjects[i] = entries[i].Identity
	}
	if withDetails {
		users, err := store.New(tx).ListUsersBySubjects(ctx, subjects...)
		if err != nil {
			return nil, err
		}
		for i, u := range users {
			cE[i].IdentityDetails = &UserMetadata{
				ID:           u.ID,
				Subject:      u.Subject,
				Email:        u.Email,
				FriendlyName: u.FriendlyName,
			}
		}
	}
	return cE, err
}

type LoginArgs struct {
	Subject    string
	SigningKey []byte
	Password   string
	JWTSecret  string
	JWTExpiry  string
}

func (s *service) GetServiceName() string {
	return "accessservice"
}

func (s *service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
