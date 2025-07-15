import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { activityKeys } from '../lib/queryKeys';

export function useActivityRequests(limit?: number) {
  return useQuery({
    queryKey: activityKeys.requests(limit),
    queryFn: () => api.getActivityRequests(limit),
    refetchInterval: 5000,
  });
}
