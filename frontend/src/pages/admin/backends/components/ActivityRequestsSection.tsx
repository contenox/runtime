import { EmptyState, Panel, Spinner, Table } from '@contenox/ui';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { useActivityRequests } from '../../../../hooks/useActivityRequests';
import RequestRow from './RequestRow';

export default function ActivityRequestsSection() {
  const { t } = useTranslation();
  const { data: requests, isLoading, isError, error } = useActivityRequests(100);
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
        {t('activity.error_fetching_requests')}: {error.message}
      </Panel>
    );
  }

  if (!requests || requests.length === 0) {
    return (
      <EmptyState
        title={t('activity.requests_empty_title')}
        description={t('activity.requests_empty_description')}
      />
    );
  }

  return (
    <div className="overflow-auto">
      <Table columns={[t('activity.request_id'), t('activity.status'), t('activity.events')]}>
        {requests.map(request => (
          <RequestRow
            key={request.id}
            request={request}
            isExpanded={expandedRequest === request.id}
            onToggle={toggleRequest}
          />
        ))}
      </Table>
    </div>
  );
}
