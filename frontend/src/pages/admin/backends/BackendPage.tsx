import { TabbedPage } from '@cate/ui';
import { useTranslation } from 'react-i18next';
import { useDownloadProgressSSE } from '../../../hooks/useDownload';
import { useQueue } from '../../../hooks/useQueue';
import BackendsSection from './components/BackendsSection';
import PoolsSection from './components/PoolsSection';

export default function BackendsPage() {
  const { t } = useTranslation();
  const { data: queue, isLoading, isError, error } = useQueue();
  const { statusMap } = useDownloadProgressSSE();

  const tabs = [
    {
      id: 'backends',
      label: t('backends.manage_title'),
      content: <BackendsSection statusMap={statusMap} />,
    },
    {
      id: 'pools', // New tab
      label: t('pools.manage_title'),
      content: <PoolsSection />,
    },
    {
      id: 'state',
      label: t('state.title'),
      content: isLoading ? (
        <div>Loading...</div>
      ) : isError ? (
        <div>Error: {error?.message}</div>
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
