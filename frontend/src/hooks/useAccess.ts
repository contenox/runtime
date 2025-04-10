import {
  useMutation,
  UseMutationResult,
  useQueryClient,
  useSuspenseQuery,
} from '@tanstack/react-query';
import { api } from '../lib/api';
import { AccessEntry } from '../lib/types';

export function useAccessEntries(expand: boolean, identity?: string) {
  return useSuspenseQuery<AccessEntry[]>({
    queryKey: ['accessEntries', { expand, identity }],
    queryFn: () => api.getAccessEntries(expand, identity),
  });
}

export function usePermissions() {
  return useSuspenseQuery<string[]>({
    queryKey: ['perms'],
    queryFn: () => api.getPermissions(),
  });
}
export function useCreateAccessEntry(): UseMutationResult<
  AccessEntry,
  Error,
  Partial<AccessEntry>,
  unknown
> {
  const queryClient = useQueryClient();
  return useMutation<AccessEntry, Error, Partial<AccessEntry>>({
    mutationFn: api.createAccessEntry,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['accessEntries'] });
    },
  });
}

export function useUpdateAccessEntry() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<AccessEntry> }) => {
      return api.updateAccessEntry(id, data);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['accessEntries'] });
    },
  });
}

export function useDeleteAccessEntry(): UseMutationResult<void, Error, string, unknown> {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: api.deleteAccessEntry,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['accessEntries'] });
    },
  });
}
