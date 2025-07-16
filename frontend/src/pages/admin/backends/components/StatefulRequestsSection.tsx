import { EmptyState, Panel, Spinner, Table } from '@contenox/ui';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { useActivityStatefulRequests } from '../../../../hooks/useActitivy';
import StatefulRequestRow from './StatefulRequestRow';

export default function StatefulRequestsSection() {
  const { t } = useTranslation();
  const { data: requests, isLoading, isError, error } = useActivityStatefulRequests();
  const [searchParams] = useSearchParams();
  const initialRequestId = searchParams.get('requestId');
  const [expandedRequest, setExpandedRequest] = useState<string | null>(initialRequestId);

  const toggleRequest = (requestId: string) => {
    setExpandedRequest(expandedRequest === requestId ? null : requestId);
  };

  useEffect(() => {
    if (initialRequestId) {
      setExpandedRequest(initialRequestId);
    }
  }, [initialRequestId]);

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
        {t('activity.error_fetching_stateful_requests')}: {error.message}
      </Panel>
    );
  }

  if (!requests || requests.length === 0) {
    return (
      <EmptyState
        title={t('activity.stateful_requests_empty_title')}
        description={t('activity.stateful_requests_empty_description')}
      />
    );
  }

  return (
    <div className="overflow-auto">
      <Table columns={[t('activity.request_id'), t('activity.status'), t('activity.actions')]}>
        {requests.map(request => (
          <StatefulRequestRow
            key={request}
            request={request}
            isExpanded={expandedRequest === request}
            onToggle={toggleRequest}
          />
        ))}
      </Table>
    </div>
  );
}
