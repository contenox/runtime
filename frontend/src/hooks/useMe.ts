import { useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { AuthenticatedUser } from '../lib/types';

export function useMe() {
  return useQuery<AuthenticatedUser>({
    queryKey: ['user'],
    queryFn: api.getCurrentUser,
  });
}
