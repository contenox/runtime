package store_test

import (
	"testing"
	"time"

	libdb "github.com/contenox/dbexec"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUnit_Pools_CreateAndGetPool(t *testing.T) {
	ctx, s := store.SetupStore(t)

	pool := &store.Pool{
		ID:          uuid.NewString(),
		Name:        "TestPool",
		PurposeType: "inference",
	}

	err := s.CreatePool(ctx, pool)
	require.NoError(t, err)
	require.NotEmpty(t, pool.ID)

	got, err := s.GetPool(ctx, pool.ID)
	require.NoError(t, err)
	require.Equal(t, pool.Name, got.Name)
	require.Equal(t, pool.PurposeType, got.PurposeType)
	require.WithinDuration(t, pool.CreatedAt, got.CreatedAt, time.Second)
	require.WithinDuration(t, pool.UpdatedAt, got.UpdatedAt, time.Second)
}

func TestUnit_Pools_UpdatePool(t *testing.T) {
	ctx, s := store.SetupStore(t)

	pool := &store.Pool{
		ID:          uuid.NewString(),
		Name:        "InitialPool",
		PurposeType: "testing",
	}

	err := s.CreatePool(ctx, pool)
	require.NoError(t, err)

	pool.Name = "UpdatedPool"
	pool.PurposeType = "production"

	err = s.UpdatePool(ctx, pool)
	require.NoError(t, err)

	got, err := s.GetPool(ctx, pool.ID)
	require.NoError(t, err)
	require.Equal(t, "UpdatedPool", got.Name)
	require.Equal(t, "production", got.PurposeType)
	require.True(t, got.UpdatedAt.After(got.CreatedAt))
}

func TestUnit_Pools_DeletePool(t *testing.T) {
	ctx, s := store.SetupStore(t)

	pool := &store.Pool{
		ID:          uuid.NewString(),
		Name:        "ToDelete",
		PurposeType: "testing",
	}

	err := s.CreatePool(ctx, pool)
	require.NoError(t, err)

	err = s.DeletePool(ctx, pool.ID)
	require.NoError(t, err)

	_, err = s.GetPool(ctx, pool.ID)
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestUnit_Pools_ListPools(t *testing.T) {
	ctx, s := store.SetupStore(t)

	pools, err := s.ListPools(ctx)
	require.NoError(t, err)
	require.Empty(t, pools)

	pool1 := &store.Pool{ID: uuid.NewString(), Name: "Pool1", PurposeType: "type1"}
	pool2 := &store.Pool{ID: uuid.NewString(), Name: "Pool2", PurposeType: "type2"}

	err = s.CreatePool(ctx, pool1)
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond)
	err = s.CreatePool(ctx, pool2)
	require.NoError(t, err)

	pools, err = s.ListPools(ctx)
	require.NoError(t, err)
	require.Len(t, pools, 2)
	require.Equal(t, pool2.ID, pools[0].ID)
	require.Equal(t, pool1.ID, pools[1].ID)
}

func TestUnit_Pools_GetPoolByName(t *testing.T) {
	ctx, s := store.SetupStore(t)

	pool := &store.Pool{
		ID:          uuid.NewString(),
		Name:        "UniquePool",
		PurposeType: "inference",
	}

	err := s.CreatePool(ctx, pool)
	require.NoError(t, err)

	got, err := s.GetPoolByName(ctx, "UniquePool")
	require.NoError(t, err)
	require.Equal(t, pool.ID, got.ID)
}

func TestUnit_Pools_ListPoolsByPurpose(t *testing.T) {
	ctx, s := store.SetupStore(t)

	purpose := "inference"
	pool1 := &store.Pool{ID: uuid.NewString(), Name: "Pool1", PurposeType: purpose}
	pool2 := &store.Pool{ID: uuid.NewString(), Name: "Pool2", PurposeType: "training"}

	s.CreatePool(ctx, pool1)
	s.CreatePool(ctx, pool2)

	pools, err := s.ListPoolsByPurpose(ctx, purpose)
	require.NoError(t, err)
	require.Len(t, pools, 1)
	require.Equal(t, pool1.ID, pools[0].ID)
}

func TestUnit_Pools_AssignAndListBackendsForPool(t *testing.T) {
	ctx, s := store.SetupStore(t)

	pool := &store.Pool{ID: uuid.NewString(), Name: "Pool1"}
	s.CreatePool(ctx, pool)

	backend := &store.Backend{
		ID:      uuid.NewString(),
		Name:    "Backend1",
		BaseURL: "http://backend1",
		Type:    "ollama",
	}
	s.CreateBackend(ctx, backend)

	err := s.AssignBackendToPool(ctx, pool.ID, backend.ID)
	require.NoError(t, err)

	backends, err := s.ListBackendsForPool(ctx, pool.ID)
	require.NoError(t, err)
	require.Len(t, backends, 1)
	require.Equal(t, backend.ID, backends[0].ID)
}

func TestUnit_Pools_RemoveBackendFromPool(t *testing.T) {
	ctx, s := store.SetupStore(t)

	pool := &store.Pool{ID: uuid.NewString(), Name: "Pool1"}
	s.CreatePool(ctx, pool)

	backend := &store.Backend{ID: uuid.NewString(), Name: "Backend1"}
	s.CreateBackend(ctx, backend)

	s.AssignBackendToPool(ctx, pool.ID, backend.ID)

	err := s.RemoveBackendFromPool(ctx, pool.ID, backend.ID)
	require.NoError(t, err)

	backends, err := s.ListBackendsForPool(ctx, pool.ID)
	require.NoError(t, err)
	require.Empty(t, backends)
}

func TestUnit_Pools_ListPoolsForBackend(t *testing.T) {
	ctx, s := store.SetupStore(t)

	backend := &store.Backend{ID: uuid.NewString(), Name: "Backend1"}
	s.CreateBackend(ctx, backend)

	pool1 := &store.Pool{ID: uuid.NewString(), Name: "Pool1"}
	pool2 := &store.Pool{ID: uuid.NewString(), Name: "Pool2"}
	s.CreatePool(ctx, pool1)
	s.CreatePool(ctx, pool2)

	s.AssignBackendToPool(ctx, pool1.ID, backend.ID)
	s.AssignBackendToPool(ctx, pool2.ID, backend.ID)

	pools, err := s.ListPoolsForBackend(ctx, backend.ID)
	require.NoError(t, err)
	require.Len(t, pools, 2)
	poolIDs := map[string]bool{pool1.ID: true, pool2.ID: true}
	for _, p := range pools {
		require.True(t, poolIDs[p.ID])
	}
}

func TestUnit_Pools_AssignAndListModelsForPool(t *testing.T) {
	ctx, s := store.SetupStore(t)

	model := &store.Model{Model: "model1"}
	err := s.AppendModel(ctx, model)
	require.NoError(t, err)

	pool := &store.Pool{ID: uuid.NewString(), Name: "Pool1"}
	s.CreatePool(ctx, pool)

	err = s.AssignModelToPool(ctx, pool.ID, model.ID)
	require.NoError(t, err)

	models, err := s.ListModelsForPool(ctx, pool.ID)
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, model.ID, models[0].ID)
	// test for error when assigning model to pool twice
	err = s.AssignModelToPool(ctx, pool.ID, model.ID)
	require.Error(t, err, libdb.ErrConstraintViolation)
}

func TestUnit_Pools_RemoveModelFromPool(t *testing.T) {
	ctx, s := store.SetupStore(t)

	model := &store.Model{Model: "model1"}
	s.AppendModel(ctx, model)

	pool := &store.Pool{ID: uuid.NewString(), Name: "Pool1"}
	s.CreatePool(ctx, pool)

	s.AssignModelToPool(ctx, pool.ID, model.ID)

	err := s.RemoveModelFromPool(ctx, pool.ID, model.ID)
	require.NoError(t, err)

	models, err := s.ListModelsForPool(ctx, pool.ID)
	require.NoError(t, err)
	require.Empty(t, models)
}

func TestUnit_Pools_ListPoolsForModel(t *testing.T) {
	ctx, s := store.SetupStore(t)

	model := &store.Model{Model: "model1"}
	s.AppendModel(ctx, model)

	pool1 := &store.Pool{ID: uuid.NewString(), Name: "Pool1"}
	pool2 := &store.Pool{ID: uuid.NewString(), Name: "Pool2"}
	s.CreatePool(ctx, pool1)
	s.CreatePool(ctx, pool2)

	s.AssignModelToPool(ctx, pool1.ID, model.ID)
	s.AssignModelToPool(ctx, pool2.ID, model.ID)

	pools, err := s.ListPoolsForModel(ctx, model.ID)
	require.NoError(t, err)
	require.Len(t, pools, 2)
	poolIDs := map[string]bool{pool1.ID: true, pool2.ID: true}
	for _, p := range pools {
		require.True(t, poolIDs[p.ID])
	}
}

func TestUnit_Pools_GetNonExistentPool(t *testing.T) {
	ctx, s := store.SetupStore(t)

	_, err := s.GetPool(ctx, uuid.NewString())
	require.ErrorIs(t, err, libdb.ErrNotFound)

	_, err = s.GetPoolByName(ctx, "non-existent")
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestUnit_Pools_DuplicatePoolName(t *testing.T) {
	ctx, s := store.SetupStore(t)

	pool := &store.Pool{ID: uuid.NewString(), Name: "Duplicate"}
	err := s.CreatePool(ctx, pool)
	require.NoError(t, err)

	pool2 := &store.Pool{ID: uuid.NewString(), Name: "Duplicate"}
	err = s.CreatePool(ctx, pool2)
	require.Error(t, err)
}
