import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { jobKeys } from '../lib/queryKeys';
import { InProgressJob, PendingJob } from '../lib/types';

export function usePendingJobs(cursor?: Date) {
  return useQuery<PendingJob[]>({
    queryKey: [...jobKeys.pending(), { cursor }],
    queryFn: () => api.listPendingJobs(cursor?.toISOString()),
    refetchInterval: 2000,
  });
}

export function useInProgressJobs(cursor?: Date) {
  return useQuery<InProgressJob[]>({
    queryKey: [...jobKeys.inprogress(), { cursor }],
    queryFn: () => api.listInProgressJobs(cursor?.toISOString()),
    refetchInterval: 2000,
  });
}
