import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { InProgressJob, PendingJob } from '../lib/types';

const jobKeys = {
  pending: 'pending',
  inprogress: 'inprogress',
};

export function usePendingJobs(cursor?: Date) {
  return useQuery<PendingJob[]>({
    queryKey: [...jobKeys.pending, { cursor }],
    queryFn: () => api.listPendingJobs(cursor?.toISOString()),
  });
}

export function useInProgressJobs(cursor?: Date) {
  return useQuery<InProgressJob[]>({
    queryKey: [...jobKeys.inprogress, { cursor }],
    queryFn: () => api.listInProgressJobs(cursor?.toISOString()),
  });
}
