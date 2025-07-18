import {
  useMutation,
  UseMutationResult,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';
import { api } from '../lib/api';
import { jobKeys } from '../lib/queryKeys';
import { Job } from '../lib/types';

export function useQueue() {
  return useSuspenseQuery<Job[] | null>({
    queryKey: jobKeys.all,
    queryFn: api.getQueue,
  });
}

export function useDeleteQueueEntry(): UseMutationResult<void, Error, string, unknown> {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: api.deleteQueueEntry,
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: jobKeys.all });
      queryClient.invalidateQueries({ queryKey: jobKeys.pending() });
      queryClient.invalidateQueries({ queryKey: jobKeys.inprogress() });
    },
  });
}
