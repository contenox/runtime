import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { activityKeys } from '../lib/queryKeys';
export function useActivityLogs(limit?: number) {
  return useQuery({
    queryKey: activityKeys.list(limit),
    queryFn: () => api.getActivityLogs(limit),
    refetchInterval: 5000,
  });
}
