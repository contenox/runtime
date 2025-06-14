import {
  useMutation,
  UseMutationResult,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';
import { api } from '../lib/api';
import { modelKeys } from '../lib/queryKeys';
import { Model, ModelListResponse } from '../lib/types';

export function useModels() {
  return useSuspenseQuery<ModelListResponse>({
    queryKey: modelKeys.all,
    queryFn: api.getModels,
  });
}

export function useCreateModel(): UseMutationResult<Model, Error, string, unknown> {
  const queryClient = useQueryClient();
  return useMutation<Model, Error, string>({
    mutationFn: api.createModel,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: modelKeys.all });
    },
  });
}

export function useDeleteModel(): UseMutationResult<void, Error, string, unknown> {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: api.deleteModel,
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: modelKeys.all });
    },
  });
}

export function useRemoveModelFromQueue(): UseMutationResult<void, Error, string, unknown> {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: api.removeModelFromQueue,
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: modelKeys.all });
    },
  });
}

// export function useCancelQueue(): UseMutationResult<void, Error, string, unknown> {
//   const queryClient = useQueryClient();
//   return useMutation<void, Error, string>({
//     mutationFn: api.cancelQueue,
//     onSettled: () => {
//       queryClient.invalidateQueries({ queryKey: ['state'] });
//     },
//   });
// }
