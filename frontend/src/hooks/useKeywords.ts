import { useSuspenseQuery } from '@tanstack/react-query';
import { api } from '../lib/api';

export function useKeywords() {
  return useSuspenseQuery<string[]>({
    queryKey: ['keywords'],
    queryFn: () => api.getKeywords(),
  });
}
