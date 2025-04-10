import { useSuspenseQuery } from '@tanstack/react-query';
import { api } from '../lib/api';

export function useSystemServices() {
  return useSuspenseQuery<string[]>({
    queryKey: ['getSystemServices'],
    queryFn: api.getSystemServices,
  });
}
