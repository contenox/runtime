import { EmptyState, Panel, Span, Spinner, Table, TableCell, TableRow } from '@contenox/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  useActivityOperations,
  useActivityRequestsByOperation,
} from '../../../../hooks/useActitivy';
import { Operation } from '../../../../lib/types';
import RequestRow from './RequestRow';

export default function ActivityOperationsSection() {
  const { t } = useTranslation();
  const { data: operations, isLoading, isError, error } = useActivityOperations();
  const [expandedOperation, setExpandedOperation] = useState<string | null>(null);
  const [expandedRequest, setExpandedRequest] = useState<string | null>(null);

  const toggleOperation = (operationKey: string) => {
    if (expandedOperation === operationKey) {
      setExpandedOperation(null);
      setExpandedRequest(null);
    } else {
      setExpandedOperation(operationKey);
      setExpandedRequest(null);
    }
  };

  const toggleRequest = (requestId: string) => {
    setExpandedRequest(expandedRequest === requestId ? null : requestId);
  };

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
        {t('activity.error_fetching_operations')}: {error.message}
      </Panel>
    );
  }

  if (!operations || operations.length === 0) {
    return (
      <EmptyState
        title={t('activity.operations_empty_title')}
        description={t('activity.operations_empty_description')}
      />
    );
  }

  return (
    <div className="overflow-auto">
      <Table columns={[t('activity.operation'), t('activity.subject'), t('activity.actions')]}>
        {operations.map(operation => {
          const operationKey = `${operation.operation}:${operation.subject}`;
          return (
            <OperationRow
              key={operationKey}
              operation={operation}
              isExpanded={expandedOperation === operationKey}
              onToggle={() => toggleOperation(operationKey)}
              expandedRequest={expandedRequest}
              onRequestToggle={toggleRequest}
            />
          );
        })}
      </Table>
    </div>
  );
}

function OperationRow({
  operation,
  isExpanded,
  onToggle,
  expandedRequest,
  onRequestToggle,
}: {
  operation: Operation;
  isExpanded: boolean;
  onToggle: () => void;
  expandedRequest: string | null;
  onRequestToggle: (id: string) => void;
}) {
  const { t } = useTranslation();
  const {
    data: requests,
    isLoading,
    isError,
  } = useActivityRequestsByOperation(operation.operation, operation.subject, {
    enabled: isExpanded,
  });

  return (
    <>
      <TableRow onClick={onToggle} className="cursor-pointer">
        <TableCell>
          <Span>{operation.operation}</Span>
        </TableCell>
        <TableCell>
          <Span>{operation.subject}</Span>
        </TableCell>
        <TableCell>
          {isExpanded ? t('activity.hide_requests') : t('activity.show_requests')}
        </TableCell>
      </TableRow>

      {isExpanded && (
        <TableRow>
          <TableCell colSpan={3}>
            {isLoading && (
              <div className="flex justify-center p-4">
                <Spinner size="md" />
              </div>
            )}

            {isError && (
              <Panel variant="error" className="my-2">
                {t('activity.error_fetching_operation_requests')}
              </Panel>
            )}

            {requests && requests.length > 0 ? (
              <div className="ml-4 border-l-2 pl-4">
                <Table
                  columns={[t('activity.request_id'), t('activity.status'), t('activity.events')]}>
                  {requests.map(request => (
                    <RequestRow
                      key={request.id}
                      request={request}
                      isExpanded={expandedRequest === request.id}
                      onToggle={onRequestToggle}
                    />
                  ))}
                </Table>
              </div>
            ) : (
              <div className="ml-4 py-2 pl-4 text-gray-500">
                {t('activity.no_requests_for_operation')}
              </div>
            )}
          </TableCell>
        </TableRow>
      )}
    </>
  );
}
