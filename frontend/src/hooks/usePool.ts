import { useMutation, useQueryClient, useSuspenseQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { Backend, Model, Pool } from '../lib/types';

// Pool CRUD hooks
export function usePools() {
  return useSuspenseQuery<Pool[]>({
    queryKey: ['pools'],
    queryFn: () => api.getPools(),
  });
}

export function usePool(id: string) {
  return useSuspenseQuery<Pool>({
    queryKey: ['pools', id],
    queryFn: () => api.getPool(id),
  });
}

export function useCreatePool() {
  const queryClient = useQueryClient();
  return useMutation<Pool, Error, Partial<Pool>>({
    mutationFn: api.createPool,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['pools'] });
    },
  });
}

export function useUpdatePool() {
  const queryClient = useQueryClient();
  return useMutation<Pool, Error, { id: string; data: Partial<Pool> }>({
    mutationFn: ({ id, data }) => api.updatePool(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['pools'] });
    },
  });
}

export function useDeletePool() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: api.deletePool,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['pools'] });
    },
  });
}

// Association hooks
export function useAssignBackendToPool() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { poolID: string; backendID: string }>({
    mutationFn: ({ poolID, backendID }) => api.assignBackendToPool(poolID, backendID),
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({ queryKey: ['pools', variables.poolID, 'backends'] });
      queryClient.invalidateQueries({ queryKey: ['backends', variables.backendID, 'pools'] });
    },
  });
}

export function useBackendsForPool(poolID: string) {
  return useSuspenseQuery<Backend[]>({
    queryKey: ['pools', poolID, 'backends'],
    queryFn: () => api.listBackendsForPool(poolID),
  });
}

export function usePoolsForBackend(backendID: string) {
  return useSuspenseQuery<Pool[]>({
    queryKey: ['backends', backendID, 'pools'],
    queryFn: () => api.listPoolsForBackend(backendID),
  });
}
// Similar hooks for model associations
export function useAssignModelToPool() {
  const queryClient = useQueryClient();
  return useMutation<void, Error, { poolID: string; modelID: string }>({
    mutationFn: ({ poolID, modelID }) => api.assignModelToPool(poolID, modelID),
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({ queryKey: ['pools', variables.poolID, 'models'] });
      queryClient.invalidateQueries({ queryKey: ['models', variables.modelID, 'pools'] });
    },
  });
}

export function useModelsForPool(poolID: string) {
  return useSuspenseQuery<Model[]>({
    queryKey: ['pools', poolID, 'models'],
    queryFn: () => api.listModelsForPool(poolID),
  });
}

// Additional utility hooks
export function usePoolsByPurpose(purpose: string) {
  return useSuspenseQuery<Pool[]>({
    queryKey: ['pools', 'purpose', purpose],
    queryFn: () => api.listPoolsByPurpose(purpose),
  });
}

export function usePoolByName(name: string) {
  return useSuspenseQuery<Pool>({
    queryKey: ['pools', 'name', name],
    queryFn: () => api.getPoolByName(name),
  });
}
