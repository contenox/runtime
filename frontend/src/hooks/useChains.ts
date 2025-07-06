import {
  useMutation,
  UseMutationResult,
  useQuery,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';
import { api } from '../lib/api';
import { chainKeys } from '../lib/queryKeys';
import { ChainDefinition, ChainTask, Trigger } from '../lib/types';

// Fetch all chains
export function useChains() {
  return useSuspenseQuery<ChainDefinition[]>({
    queryKey: chainKeys.list(),
    queryFn: api.getChains,
  });
}

// Fetch single chain
export function useChain(id: string) {
  return useQuery<ChainDefinition>({
    queryKey: chainKeys.detail(id),
    queryFn: () => api.getChain(id),
    enabled: !!id,
  });
}

// Create new chain
export function useCreateChain(): UseMutationResult<
  ChainDefinition,
  Error,
  ChainDefinition,
  unknown
> {
  const queryClient = useQueryClient();
  return useMutation<ChainDefinition, Error, ChainDefinition>({
    mutationFn: data => api.createChain(data),
    onSuccess: newChain => {
      queryClient.invalidateQueries({ queryKey: chainKeys.list() });
      queryClient.setQueryData(chainKeys.detail(newChain.id), newChain);
    },
  });
}

// Update existing chain
export function useUpdateChain(id: string) {
  const queryClient = useQueryClient();
  return useMutation<ChainDefinition, Error, Partial<ChainDefinition>, unknown>({
    mutationFn: data => api.updateChain(id, data),
    onSuccess: updatedChain => {
      queryClient.invalidateQueries({ queryKey: chainKeys.list() });
      queryClient.setQueryData(chainKeys.detail(id), updatedChain);
    },
  });
}

// Delete chain
export function useDeleteChain(): UseMutationResult<void, Error, string, unknown> {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: id => api.deleteChain(id),
    onSuccess: (_, id) => {
      queryClient.invalidateQueries({ queryKey: chainKeys.list() });
      queryClient.removeQueries({ queryKey: chainKeys.detail(id) });
    },
  });
}

// Fetch triggers for a chain
export function useChainTriggers(chainId: string) {
  return useQuery<Trigger[]>({
    queryKey: chainKeys.triggers(chainId),
    queryFn: () => api.getChainTriggers(chainId),
    enabled: !!chainId,
  });
}

// Add trigger to chain
export function useAddChainTrigger(chainId: string) {
  const queryClient = useQueryClient();
  return useMutation<Trigger, Error, Trigger>({
    mutationFn: data => api.addChainTrigger(chainId, data),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: chainKeys.triggers(chainId),
      });
    },
  });
}

// Remove trigger from chain
export function useRemoveChainTrigger(chainId: string) {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: triggerId => api.removeChainTrigger(chainId, triggerId),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: chainKeys.triggers(chainId),
      });
    },
  });
}

export function useUpdateChainTask(chainId: string) {
  const queryClient = useQueryClient();
  return useMutation<ChainDefinition, Error, { taskId: string; data: Partial<ChainTask> }, unknown>(
    {
      mutationFn: async ({ taskId, data }) => {
        const chain = await queryClient.fetchQuery({
          queryKey: chainKeys.detail(chainId),
          queryFn: () => api.getChain(chainId),
        });

        const updatedTasks = chain.tasks.map(task =>
          task.id === taskId ? { ...task, ...data } : task,
        );

        return api.updateChain(chainId, { tasks: updatedTasks });
      },
      onSuccess: updatedChain => {
        queryClient.setQueryData(chainKeys.detail(chainId), updatedChain);
      },
    },
  );
}

// Add task to chain
export function useAddChainTask(chainId: string) {
  const queryClient = useQueryClient();
  return useMutation<ChainDefinition, Error, ChainTask, unknown>({
    mutationFn: async newTask => {
      const chain = await queryClient.fetchQuery({
        queryKey: chainKeys.detail(chainId),
        queryFn: () => api.getChain(chainId),
      });

      return api.updateChain(chainId, {
        tasks: [...chain.tasks, newTask],
      });
    },
    onSuccess: updatedChain => {
      queryClient.setQueryData(chainKeys.detail(chainId), updatedChain);
    },
  });
}

// Remove task from chain
export function useRemoveChainTask(chainId: string) {
  const queryClient = useQueryClient();
  return useMutation<
    ChainDefinition,
    Error,
    string, // taskId
    unknown
  >({
    mutationFn: async taskId => {
      const chain = await queryClient.fetchQuery({
        queryKey: chainKeys.detail(chainId),
        queryFn: () => api.getChain(chainId),
      });

      return api.updateChain(chainId, {
        tasks: chain.tasks.filter(task => task.id !== taskId),
      });
    },
    onSuccess: updatedChain => {
      queryClient.setQueryData(chainKeys.detail(chainId), updatedChain);
    },
  });
}
