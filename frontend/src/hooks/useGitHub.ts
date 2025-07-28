import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import { githubKeys } from '../lib/queryKeys';

export function useListGitHubRepos() {
  return useQuery({
    queryKey: githubKeys.repos(),
    queryFn: api.listGitHubRepos,
  });
}

export function useConnectGitHubRepo() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (data: {
      userID: string;
      owner: string;
      botUserName: string;
      repoName: string;
      accessToken: string;
    }) => api.connectGitHubRepo(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: githubKeys.repos() });
    },
    onSettled: () => {
      // Ensures sensitive data is not retained in mutation cache
    },
  });
}

export function useDeleteGitHubRepo() {
  const queryClient = useQueryClient();

  return useMutation<void, Error, string>({
    mutationFn: (repoID: string) => api.deleteGitHubRepo(repoID),
    onSuccess: (_, repoID) => {
      queryClient.invalidateQueries({ queryKey: githubKeys.repo(repoID) });
      queryClient.invalidateQueries({ queryKey: githubKeys.repos() });
    },
  });
}

export function useListGitHubPRs(repoID: string) {
  return useQuery({
    queryKey: githubKeys.prs(repoID),
    queryFn: () => api.listGitHubPRs(repoID),
    enabled: !!repoID,
  });
}
