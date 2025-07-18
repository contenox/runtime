import { Span, Table, TableCell, TableRow } from '@contenox/ui';
import { t } from 'i18next';
import { ActivityLogsResponse } from '../../../../lib/types';

export default function EventsRow({ logs }: { logs: ActivityLogsResponse }) {
  const truncateString = (str: string, maxLength: number) =>
    str.length > maxLength ? `${str.slice(0, maxLength)}...` : str;

  // Format entity_data as JSON string
  const formatEntityData = (data?: Record<string, undefined>) =>
    data ? JSON.stringify(data, null, 2) : '';

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
