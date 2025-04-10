import {
  useMutation,
  UseMutationResult,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';
import { api } from '../lib/api';
import { Job } from '../lib/types';

export function useQueue() {
  return useSuspenseQuery<Job[] | null>({
    queryKey: ['jobs'],
    queryFn: api.getQueue,
  });
}

export function useDeleteQueueEntry(): UseMutationResult<void, Error, string, unknown> {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: api.deleteQueueEntry,
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ['jobs'] });
    },
  });
}
