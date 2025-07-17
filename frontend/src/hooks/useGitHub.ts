import { useMutation, useQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { githubKeys } from '../lib/queryKeys';

export function useListGitHubRepos() {
  return useQuery({
    queryKey: githubKeys.repos(),
    queryFn: api.listGitHubRepos,
  });
}

export function useConnectGitHubRepo() {
  return useMutation({
    mutationFn: (data: { userID: string; owner: string; repoName: string; accessToken: string }) =>
      api.connectGitHubRepo(data),
  });
}

export function useDeleteGitHubRepo() {
  return useMutation({
    mutationFn: (repoID: string) => api.deleteGitHubRepo(repoID),
  });
}

export function useListGitHubPRs(repoID: string) {
  return useQuery({
    queryKey: githubKeys.prs(repoID),
    queryFn: () => api.listGitHubPRs(repoID),
    enabled: !!repoID,
  });
}
