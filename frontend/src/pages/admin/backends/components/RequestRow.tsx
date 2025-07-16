import { Panel, Span, Spinner, Table, TableCell, TableRow } from '@contenox/ui';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { api } from '../../../../lib/api';
import { ActivityLog, TrackedRequest } from '../../../../lib/types';

interface RequestRowProps {
  request: TrackedRequest;
  isExpanded: boolean;
  onToggle: (id: string) => void;
}

export default function RequestRow({ request, isExpanded, onToggle }: RequestRowProps) {
  const { t } = useTranslation();
  const {
    data: events,
    isLoading,
    isError,
  } = useQuery({
    queryKey: ['activity-request', request.id],
    queryFn: () => api.getActivityRequestById(request.id),
    enabled: isExpanded,
  });

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
    <>
      <TableRow onClick={() => onToggle(request.id)} className="cursor-pointer">
        <TableCell>
          <Span>{request.id}</Span>
        </TableCell>
        <TableCell>{isExpanded ? t('activity.hide_events') : t('activity.show_events')}</TableCell>
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
                {t('activity.error_fetching_request_events')}
              </Panel>
            )}

            {events && events.length > 0 && (
              <div className="ml-4 border-l-2 pl-4">
                <Table
                  columns={[
                    t('activity.operation'),
                    t('activity.subject'),
                    t('activity.start_time'),
                    t('activity.status'),
                  ]}>
                  {events.map((event: ActivityLog) => (
                    <TableRow key={event.id}>
                      <TableCell>
                        <Span>{event.operation}</Span>
                      </TableCell>
                      <TableCell>
                        <Span>{event.subject}</Span>
                      </TableCell>
                      <TableCell>
                        <Span>{formatDateTime(event.start)}</Span>
                      </TableCell>
                      <TableCell>
                        {event.error ? (
                          <Span variant="status" className="text-error">
                            {t('activity.failed')}
                          </Span>
                        ) : (
                          <Span variant="status" className="text-success">
                            {t('activity.success')}
                          </Span>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </Table>
              </div>
            )}
          </TableCell>
        </TableRow>
      )}
    </>
  );
}
