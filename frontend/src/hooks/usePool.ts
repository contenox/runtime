import { useMutation, useQueryClient, useSuspenseQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { backendKeys, modelKeys, poolKeys } from '../lib/queryKeys';
import { Backend, Model, Pool } from '../lib/types';

// Pool CRUD hooks
export function usePools() {
  return useSuspenseQuery<Pool[]>({
    queryKey: poolKeys.all,
    queryFn: () => api.getPools(),
  });
}

export function usePool(id: string) {
  return useSuspenseQuery<Pool>({
    queryKey: poolKeys.detail(id),
    queryFn: () => api.getPool(id),
  });
}

export function useCreatePool() {
  const queryClient = useQueryClient();
  return useMutation<Pool, Error, Partial<Pool>>({
    mutationFn: api.createPool,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: poolKeys.all });
    },
  });
}

export function useUpdatePool() {
  const queryClient = useQueryClient();
  return useMutation<Pool, Error, { id: string; data: Partial<Pool> }>({
    mutationFn: ({ id, data }) => api.updatePool(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: poolKeys.all });
    },
  });
}

export function useDeletePool() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: api.deletePool,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: poolKeys.all });
    },
  });
}

// Association hooks
export function useAssignBackendToPool() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { poolID: string; backendID: string }>({
    mutationFn: ({ poolID, backendID }) => api.assignBackendToPool(poolID, backendID),
    onSuccess: (_, { poolID, backendID }) => {
      queryClient.invalidateQueries({ queryKey: backendKeys.pools(backendID) });
      queryClient.invalidateQueries({ queryKey: poolKeys.backends(poolID) });
      queryClient.invalidateQueries({ queryKey: backendKeys.all });
    },
  });
}

export function useBackendsForPool(poolID: string) {
  return useSuspenseQuery<Backend[]>({
    queryKey: poolKeys.backends(poolID),
    queryFn: () => api.listBackendsForPool(poolID),
  });
}

export function usePoolsForBackend(backendID: string) {
  return useSuspenseQuery<Pool[]>({
    queryKey: backendKeys.pools(backendID),
    queryFn: () => api.listPoolsForBackend(backendID),
  });
}

export function useAssignModelToPool() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { poolID: string; modelID: string }>({
    mutationFn: ({ poolID, modelID }) => api.assignModelToPool(poolID, modelID),
    onSuccess: (_, { poolID, modelID }) => {
      queryClient.invalidateQueries({ queryKey: poolKeys.models(poolID) });
      queryClient.invalidateQueries({ queryKey: modelKeys.pools(modelID) });
    },
  });
}

export function useModelsForPool(poolID: string) {
  return useSuspenseQuery<Model[]>({
    queryKey: poolKeys.models(poolID),
    queryFn: () => api.listModelsForPool(poolID),
  });
}

// Additional utility hooks
export function usePoolsByPurpose(purpose: string) {
  return useSuspenseQuery<Pool[]>({
    queryKey: poolKeys.byPurpose(purpose),
    queryFn: () => api.listPoolsByPurpose(purpose),
  });
}

export function usePoolByName(name: string) {
  return useSuspenseQuery<Pool>({
    queryKey: poolKeys.byName(name),
    queryFn: () => api.getPoolByName(name),
  });
}

export function useRemoveBackendFromPool() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { poolID: string; backendID: string }>({
    mutationFn: ({ poolID, backendID }) => api.removeBackendFromPool(poolID, backendID),
    onSuccess: (_, { poolID, backendID }) => {
      queryClient.invalidateQueries({ queryKey: poolKeys.backends(poolID) });
      queryClient.invalidateQueries({ queryKey: backendKeys.pools(backendID) });
    },
  });
}

export function useRemoveModelFromPool() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { poolID: string; modelID: string }>({
    mutationFn: ({ poolID, modelID }) => api.removeModelFromPool(poolID, modelID),
    onSuccess: (_, { poolID, modelID }) => {
      queryClient.invalidateQueries({ queryKey: poolKeys.models(poolID) });
      queryClient.invalidateQueries({ queryKey: modelKeys.pools(modelID) });
    },
  });
}

export function usePoolsForModel(modelID: string) {
  return useSuspenseQuery<Pool[]>({
    queryKey: modelKeys.pools(modelID),
    queryFn: () => api.listPoolsForModel(modelID),
  });
}
