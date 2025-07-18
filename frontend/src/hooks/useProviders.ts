import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import { providerKeys } from '../lib/queryKeys';

export function useProviderStatus(provider: 'openai' | 'gemini') {
  return useQuery({
    queryKey: providerKeys.status(provider),
    queryFn: () => api.getProviderStatus(provider),
    refetchInterval: 5000, // Refresh every 5 seconds
  });
}

export function useConfigureProvider(provider: 'openai' | 'gemini') {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (data: { apiKey: string; upsert: boolean }) =>
      api.configureProvider(provider, data),
    onSuccess: () => {
      // Invalidate the provider status query
      queryClient.invalidateQueries({
        queryKey: providerKeys.status(provider),
      });
    },
  });
}
