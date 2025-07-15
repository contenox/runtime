import { TabbedPage } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import { useDownloadProgressSSE } from '../../../hooks/useDownload';
import { useQueue } from '../../../hooks/useQueue';
import ActivityLogsSection from './components/ActivityLogsSection';
import ActivityRequestsSection from './components/ActivityRequestsSection';
import BackendsSection from './components/BackendsSection';
import CloudProvidersSection from './components/CloudProvidersSection';
import PoolsSection from './components/PoolsSection';

export default function BackendsPage() {
  const { t } = useTranslation();
  const {
    data: queue,
    isLoading: queueLoading,
    isError: queueError,
    error: queueErrorObj,
  } = useQueue();
  const { statusMap } = useDownloadProgressSSE();

  const tabs = [
    {
      id: 'backends',
      label: t('backends.manage_title'),
      content: <BackendsSection statusMap={statusMap} />,
    },
    {
      id: 'pools',
      label: t('pools.manage_title'),
      content: <PoolsSection />,
    },
    {
      id: 'cloud-providers',
      label: t('cloud_providers.title'),
      content: <CloudProvidersSection />,
    },
    {
      id: 'activity-logs',
      label: t('activity.logs_title'),
      content: <ActivityLogsSection />,
    },
    {
      id: 'requests',
      label: t('activity.requests_title'),
      content: <ActivityRequestsSection />,
    },
    {
      id: 'state',
      label: t('state.title'),
      content: queueLoading ? (
        <div>Loading...</div>
      ) : queueError ? (
        <div>Error: {queueErrorObj?.message}</div>
      ) : (
        <>
          <div>{queue?.length} items in queue</div>
          {queue?.map(item => (
            <div key={item.id}>
              {item.taskType}-{item.modelJob?.url || 'N/A'}-{item.modelJob?.model || 'N/A'}
            </div>
          ))}
        </>
      ),
    },
  ];

  return <TabbedPage tabs={tabs} />;
}
