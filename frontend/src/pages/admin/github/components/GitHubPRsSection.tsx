import {
  EmptyState,
  Panel,
  Section,
  Select,
  Spinner,
  Table,
  TableCell,
  TableRow,
} from '@contenox/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useListGitHubPRs, useListGitHubRepos } from '../../../../hooks/useGitHub';

export default function GitHubPRsSection() {
  const { t } = useTranslation();
  const [repoID, setRepoID] = useState('');
  const { data: repos } = useListGitHubRepos();
  const { data: prs, isLoading, isError, error } = useListGitHubPRs(repoID);

  const repoOptions =
    repos?.map(repo => ({
      value: repo.id,
      label: `${repo.owner}/${repo.repoName}`,
    })) || [];

  if (!repoOptions.length) {
    return <EmptyState title={t('github.no_repos_for_prs')} description={''} />;
  }

  return (
    <Section>
      <Select
        options={repoOptions}
        value={repoID}
        placeholder={t('github.pr_input_placeholder')}
        onChange={e => setRepoID(e.target.value)}
      />
      {isLoading ? (
        <Spinner size="lg" />
      ) : isError ? (
        <Panel variant="error">{error?.message}</Panel>
      ) : !prs?.length ? (
        <EmptyState title={t('github.no_prs')} description={''} />
      ) : (
        <Table
          columns={[
            t('github.pr_number'),
            t('github.title'),
            t('github.author'),
            t('github.state'),
          ]}>
          {prs.map(pr => (
            <TableRow key={pr.id}>
              <TableCell>#{pr.number}</TableCell>
              <TableCell>{pr.title}</TableCell>
              <TableCell>{pr.authorLogin}</TableCell>
              <TableCell>{pr.state}</TableCell>
            </TableRow>
          ))}
        </Table>
      )}
    </Section>
  );
}
