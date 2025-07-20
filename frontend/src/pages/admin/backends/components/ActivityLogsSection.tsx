import { EmptyState, Panel, Spinner } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import { useActivityLogs } from '../../../../hooks/useActitivy';
import EventsRow from './EventsRow';

export default function ActivityLogsSection() {
  const { t } = useTranslation();
  const { data: logs, isLoading, isError, error } = useActivityLogs(100);

  if (isLoading) {
    return (
      <div className="flex justify-center py-8">
        <Spinner size="lg" />
      </div>
    );
  }

  if (isError) {
    return (
      <Panel variant="error" className="my-4">
        {t('activity.error_fetching')}: {error.message}
      </Panel>
    );
  }

  if (!logs || logs.length === 0) {
    return (
      <EmptyState title={t('activity.empty_title')} description={t('activity.empty_description')} />
    );
  }

  return <EventsRow logs={logs} />;
}
