import { useSuspenseQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { systemKeys } from '../lib/queryKeys';

export function useSystemServices() {
  return useSuspenseQuery<string[]>({
    queryKey: systemKeys.all,
    queryFn: api.getSystemServices,
  });
}
