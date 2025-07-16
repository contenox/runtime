import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { activityKeys } from '../lib/queryKeys';

export function useActivityLogs(limit?: number) {
  return useQuery({
    queryKey: activityKeys.logs(limit),
    queryFn: () => api.getActivityLogs(limit),
    refetchInterval: 5000,
  });
}

export function useActivityRequests(limit?: number) {
  return useQuery({
    queryKey: activityKeys.requests(limit),
    queryFn: () => api.getActivityRequests(limit),
    refetchInterval: 5000,
  });
}

export function useActivityRequestById(requestID: string) {
  return useQuery({
    queryKey: activityKeys.requestById(requestID),
    queryFn: () => api.getActivityRequestById(requestID),
  });
}

export function useActivityOperations() {
  return useQuery({
    queryKey: activityKeys.operations(),
    queryFn: () => api.getActivityOperations(),
  });
}

export function useActivityRequestsByOperation(
  operation: string,
  subject: string,
  options?: { enabled?: boolean },
) {
  return useQuery({
    queryKey: activityKeys.operationsByType(operation, subject),
    queryFn: () => api.getActivityRequestByOperation(operation, subject),
    enabled: options?.enabled,
  });
}

export function useExecutionState(requestID: string) {
  return useQuery({
    queryKey: activityKeys.state(requestID),
    queryFn: () => api.getExecutionState(requestID),
    enabled: !!requestID,
    refetchInterval: 5000,
  });
}

export function useActivityStatefulRequests() {
  return useQuery({
    queryKey: activityKeys.statefulRequests(),
    queryFn: () => api.getActivityStatefulRequests(),
    refetchInterval: 5000, // Refetch every 5 seconds
  });
}
