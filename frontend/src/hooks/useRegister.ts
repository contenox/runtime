import {
  useMutation,
  UseMutationOptions,
  UseMutationResult,
  useQueryClient,
} from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { api } from '../lib/api';
import { userKeys } from '../lib/queryKeys';
import { AuthenticatedUser, User } from '../lib/types';

export function useRegister(
  options?: UseMutationOptions<AuthenticatedUser, Error, Partial<User>, unknown>,
): UseMutationResult<AuthenticatedUser, Error, Partial<User>, unknown> {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const defaultRedirect = '/';

  return useMutation<AuthenticatedUser, Error, Partial<User>>({
    mutationFn: api.register,
    onSuccess: (data, variables, context) => {
      queryClient.invalidateQueries({ queryKey: userKeys.current() });
      if (options?.onSuccess) {
        options.onSuccess(data, variables, context);
      } else {
        navigate(defaultRedirect);
      }
    },
  });
}
