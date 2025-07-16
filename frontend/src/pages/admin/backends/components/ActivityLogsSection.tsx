import { EmptyState, Panel, Span, Spinner, Table, TableCell, TableRow } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import { useActivityLogs } from '../../../../hooks/useActitivy';

export default function ActivityLogsSection() {
  const { t } = useTranslation();
  const { data: logs, isLoading, isError, error } = useActivityLogs(100);
  const truncateString = (str: string, maxLength: number) =>
    str.length > maxLength ? `${str.slice(0, maxLength)}...` : str;

  // Format entity_data as JSON string
  const formatEntityData = (data?: Record<string, undefined>) =>
    data ? JSON.stringify(data, null, 2) : '';

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

  function formatDateTime(dateString: string): string {
    const date = new Date(dateString);
    return date.toLocaleString(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    });
  }

  return (
    <div className="overflow-auto">
      <Table
        columns={[
          t('activity.operation'),
          t('activity.subject'),
          t('activity.start_time'),
          t('activity.duration'),
          t('activity.status'),
          t('activity.request_id'),
          t('activity.entity_id'),
          t('activity.entity_data'),
          t('activity.metadata'),
        ]}>
        {logs.map(log => (
          <TableRow key={log.id}>
            <TableCell>
              <Span>{log.operation}</Span>
            </TableCell>
            <TableCell>
              <Span>{log.subject}</Span>
            </TableCell>
            <TableCell>
              <Span>{formatDateTime(log.start)}</Span>
            </TableCell>
            <TableCell>
              <Span>
                {log.durationMS === undefined
                  ? t('activity.in_progress')
                  : log.durationMS > 0
                    ? `${log.durationMS} ms`
                    : t('activity.instant')}
              </Span>
            </TableCell>
            <TableCell>
              {log.error ? (
                <Span variant="status" className="text-error">
                  {t('activity.failed')}
                </Span>
              ) : (
                <Span variant="status" className="text-success">
                  {t('activity.success')}
                </Span>
              )}
            </TableCell>
            <TableCell>
              <Span>{log.requestID}</Span>
            </TableCell>
            <TableCell>
              <Span>{log.entityID ? truncateString(log.entityID, 20) : '-'}</Span>
            </TableCell>
            <TableCell>
              <Span>{truncateString(formatEntityData(log.entityData), 30) || '-'}</Span>
            </TableCell>
            <TableCell>
              <Span>
                {log.metadata &&
                  Object.entries(log.metadata)
                    .map(([key, value]) => `${key}: ${value}`)
                    .join(', ')}
              </Span>
            </TableCell>
          </TableRow>
        ))}
      </Table>
    </div>
  );
}
