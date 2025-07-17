import { TabbedPage } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import GitHubPRsSection from './components/GitHubPRsSection';
import GitHubReposSection from './components/GitHubReposSection';

export default function GitHubPage() {
  const { t } = useTranslation();

  const tabs = [
    {
      id: 'repositories',
      label: t('github.tabs.repositories'),
      content: <GitHubReposSection />,
    },
    {
      id: 'pull-requests',
      label: t('github.tabs.pull_requests'),
      content: <GitHubPRsSection />,
    },
  ];

  return <TabbedPage tabs={tabs} />;
}
