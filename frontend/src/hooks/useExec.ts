import { useMutation, UseMutationResult, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import { execKeys } from '../lib/queryKeys';
import { Exec, ExecResp } from '../lib/types';

export function useExecPrompt(): UseMutationResult<ExecResp, Error, Exec, unknown> {
  const queryClient = useQueryClient();
  return useMutation<ExecResp, Error, Exec>({
    mutationFn: api.execPrompt,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: execKeys.all });
    },
  });
}
