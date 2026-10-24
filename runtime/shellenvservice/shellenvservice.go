package shellenvservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
)

const globalKVKey = "shellenv.global"

var ErrInvalidName = errors.New("shellenvservice: invalid environment variable name")

type Service interface {
	Get(ctx context.Context) (map[string]string, error)
	Set(ctx context.Context, vars map[string]string) error
}

func New(db libdb.DBManager) Service { return &service{db: db} }

type service struct{ db libdb.DBManager }

func (s *service) Get(ctx context.Context) (map[string]string, error) {
	store := runtimetypes.New(s.db.WithoutTransaction())
	var vars map[string]string
	if err := store.GetKV(ctx, globalKVKey, &vars); err != nil {
		if errors.Is(err, libdb.ErrNotFound) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("shellenvservice: read global env: %w", err)
	}
	if vars == nil {
		vars = map[string]string{}
	}
	return vars, nil
}

func (s *service) Set(ctx context.Context, vars map[string]string) error {
	clean := make(map[string]string, len(vars))
	for name, value := range vars {
		if !ValidEnvName(name) {
			return fmt.Errorf("%w: %q (use letters, digits and underscores; not starting with a digit)", ErrInvalidName, name)
		}
		clean[name] = value
	}
	b, err := json.Marshal(clean)
	if err != nil {
		return fmt.Errorf("shellenvservice: marshal: %w", err)
	}
	store := runtimetypes.New(s.db.WithoutTransaction())
	if err := store.SetKV(ctx, globalKVKey, b); err != nil {
		return fmt.Errorf("shellenvservice: write global env: %w", err)
	}
	return nil
}

func ValidEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
