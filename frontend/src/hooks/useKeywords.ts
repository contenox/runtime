import { api } from '../lib/api';

export function useDeleteQueueEntry() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (model: string) => api.removeModelFromQueue(model),
    onSuccess: () => {
      // Invalidate both job-related and keywords queries
      queryClient.invalidateQueries({ queryKey: jobKeys.pending() });
      queryClient.invalidateQueries({ queryKey: jobKeys.inprogress() });
      queryClient.invalidateQueries({ queryKey: ['keywords'] });
    },
  });
}
