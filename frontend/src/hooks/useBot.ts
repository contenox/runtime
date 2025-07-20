import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import { botKeys } from '../lib/queryKeys';
import { Bot } from '../lib/types';

// Query hook for fetching all bots
export function useBots() {
  return useQuery<Bot[]>({
    queryKey: botKeys.list(),
    queryFn: api.getBots,
  });
}

// Query hook for fetching a specific bot by ID
export function useBot(id: string, options?: { enabled?: boolean }) {
  return useQuery<Bot>({
    queryKey: botKeys.detail(id),
    queryFn: () => api.getBot(id),
    enabled: options?.enabled ?? !!id,
  });
}

// Mutation for creating a new bot
export function useCreateBot() {
  const queryClient = useQueryClient();

  return useMutation<Bot, Error, Partial<Bot>>({
    mutationFn: api.createBot,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: botKeys.list() });
    },
  });
}

// Mutation for updating an existing bot
export function useUpdateBot() {
  const queryClient = useQueryClient();

  return useMutation<Bot, Error, { id: string; data: Partial<Bot> }>({
    mutationFn: ({ id, data }) => api.updateBot(id, data),
    onSuccess: ({ id }) => {
      queryClient.invalidateQueries({ queryKey: botKeys.list() });
      queryClient.invalidateQueries({ queryKey: botKeys.detail(id) });
    },
  });
}

// Mutation for deleting a bot
export function useDeleteBot() {
  const queryClient = useQueryClient();

  return useMutation<void, Error, string>({
    mutationFn: api.deleteBot,
    onSuccess: (_, id) => {
      queryClient.invalidateQueries({ queryKey: botKeys.list() });
      queryClient.invalidateQueries({ queryKey: botKeys.detail(id) });
    },
  });
}
