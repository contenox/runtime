import {
  useMutation,
  UseMutationResult,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';
import { api } from '../lib/api';
import { telegramKeys } from '../lib/queryKeys';
import { TelegramFrontend } from '../lib/types';

export function useTelegramFrontends() {
  type NewType = TelegramFrontend;

  return useSuspenseQuery<NewType[]>({
    queryKey: telegramKeys.list(),
    queryFn: api.listTelegramFrontends,
  });
}

export function useTelegramFrontend(id: string) {
  return useSuspenseQuery<TelegramFrontend>({
    queryKey: telegramKeys.detail(id),
    queryFn: () => api.getTelegramFrontend(id),
  });
}

export function useTelegramFrontendsByUser(userId: string) {
  return useSuspenseQuery<TelegramFrontend[]>({
    queryKey: telegramKeys.byUser(userId),
    queryFn: () => api.listTelegramFrontendsByUser(userId),
  });
}

export function useCreateTelegramFrontend(): UseMutationResult<
  TelegramFrontend,
  Error,
  Partial<TelegramFrontend>,
  unknown
> {
  const queryClient = useQueryClient();
  return useMutation<TelegramFrontend, Error, Partial<TelegramFrontend>>({
    mutationFn: api.createTelegramFrontend,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: telegramKeys.all });
    },
  });
}

export function useUpdateTelegramFrontend(): UseMutationResult<
  TelegramFrontend,
  Error,
  { id: string; data: Partial<TelegramFrontend> },
  unknown
> {
  const queryClient = useQueryClient();
  return useMutation<TelegramFrontend, Error, { id: string; data: Partial<TelegramFrontend> }>({
    mutationFn: ({ id, data }) => api.updateTelegramFrontend(id, data),
    onSuccess: data => {
      queryClient.invalidateQueries({ queryKey: telegramKeys.all });
      queryClient.setQueryData(telegramKeys.detail(data.id), data);
    },
  });
}

export function useDeleteTelegramFrontend(): UseMutationResult<void, Error, string, unknown> {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: api.deleteTelegramFrontend,
    onSuccess: (_, id) => {
      queryClient.invalidateQueries({ queryKey: telegramKeys.all });
      queryClient.removeQueries({ queryKey: telegramKeys.detail(id) });
    },
  });
}
