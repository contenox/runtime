import { Panel, Span, Spinner, TableCell, TableRow } from '@contenox/ui';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { api } from '../../../../lib/api';
import { TrackedRequest } from '../../../../lib/types';
import EventsRow from './eventsRow';

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

            {events && events.length > 0 && <EventsRow logs={events} />}
          </TableCell>
        </TableRow>
      )}
    </>
  );
}
