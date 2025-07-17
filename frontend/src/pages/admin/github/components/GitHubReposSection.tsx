import {
  Button,
  EmptyState,
  Form,
  FormField,
  H3,
  Input,
  Panel,
  Section,
  Spinner,
} from '@contenox/ui';
import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  useConnectGitHubRepo,
  useDeleteGitHubRepo,
  useListGitHubRepos,
} from '../../../../hooks/useGitHub';
import { GitHubRepo } from '../../../../lib/types';

export default function GitHubReposSection() {
  const { t } = useTranslation();
  const { data: repos, isLoading, error } = useListGitHubRepos();
  const connectRepoMutation = useConnectGitHubRepo();
  const deleteRepoMutation = useDeleteGitHubRepo();

  const [userId, setUserId] = useState('');
  const [owner, setOwner] = useState('');
  const [repoName, setRepoName] = useState('');
  const [accessToken, setAccessToken] = useState('');
  const [deletingRepoId, setDeletingRepoId] = useState<string | null>(null);

  const handleConnectRepo = (e: React.FormEvent) => {
    e.preventDefault();
    connectRepoMutation.mutate(
      { userID: userId, owner, repoName, accessToken },
      {
        onSuccess: () => {
          setUserId('');
          setOwner('');
          setRepoName('');
          setAccessToken('');
        },
      },
    );
  };

  const handleDeleteRepo = (repoId: string) => {
    setDeletingRepoId(repoId);
    deleteRepoMutation.mutate(repoId, {
      onSettled: () => setDeletingRepoId(null),
    });
  };

  return (
    <div className="space-y-6">
      <Section>
        <Form
          title={t('github.connect_repo')}
          onSubmit={handleConnectRepo}
          error={connectRepoMutation.isError ? t('github.connect_error') : undefined}
          actions={
            <Button type="submit" variant="primary" disabled={connectRepoMutation.isPending}>
              {connectRepoMutation.isPending ? t('common.connecting') : t('github.connect_action')}
            </Button>
          }>
          <FormField label={t('github.user_id')} required>
            <Input value={userId} onChange={e => setUserId(e.target.value)} placeholder="user-id" />
          </FormField>

          <FormField label={t('github.owner')} required>
            <Input
              value={owner}
              onChange={e => setOwner(e.target.value)}
              placeholder="organization"
            />
          </FormField>

          <FormField label={t('github.repo_name')} required>
            <Input
              value={repoName}
              onChange={e => setRepoName(e.target.value)}
              placeholder="repository-name"
            />
          </FormField>

          <FormField label={t('github.access_token')} required>
            <Input
              type="password"
              value={accessToken}
              onChange={e => setAccessToken(e.target.value)}
              placeholder="ghp_xxxxxxxxxxxxxxxxxxxx"
            />
          </FormField>
        </Form>
      </Section>

      <Section>
        <h2 className="mb-4 text-lg font-semibold">{t('github.connected_repos')}</h2>

        {isLoading && (
          <div className="flex justify-center py-8">
            <Spinner />
          </div>
        )}

        {error && <Panel variant="error">{t('github.list_error')}</Panel>}

        {!isLoading && !error && (!repos || repos.length === 0) ? (
          <EmptyState
            title={t('github.no_repos_title')}
            description={t('github.no_repos_description')}
          />
        ) : (
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
            {repos?.map(repo => (
              <RepoCard
                key={repo.id}
                repo={repo}
                onDelete={handleDeleteRepo}
                isDeleting={deleteRepoMutation.isPending && deletingRepoId === repo.id}
              />
            ))}
          </div>
        )}
      </Section>
    </div>
  );
}

type RepoCardProps = {
  repo: GitHubRepo;
  onDelete: (repoId: string) => void;
  isDeleting: boolean;
};

function RepoCard({ repo, onDelete, isDeleting }: RepoCardProps) {
  const { t } = useTranslation();

  return (
    <Section className="flex h-full flex-col">
      <div className="flex-grow">
        <H3 className="mb-2">
          {repo.owner}/{repo.repoName}
        </H3>
        <div className="space-y-1 text-sm">
          <div>
            <span className="font-medium">{t('github.user_id')}:</span> {repo.userID}
          </div>
          <div>
            <span className="font-medium">{t('common.created')}:</span>{' '}
            {new Date(repo.createdAt).toLocaleDateString()}
          </div>
        </div>
      </div>

      <div className="mt-4 flex justify-end">
        <Button
          variant="secondary"
          size="sm"
          disabled={isDeleting}
          onClick={() => onDelete(repo.id)}>
          {isDeleting ? <Spinner size="sm" /> : t('common.delete')}
        </Button>
      </div>
    </Section>
  );
}
