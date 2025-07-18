import { Panel, Span, Spinner, TableCell, TableRow } from '@contenox/ui';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useExecutionState } from '../../../../hooks/useActitivy';
import { api } from '../../../../lib/api';
import EventsRow from './EventsRow';
import StateRow from './StateRow';

interface StatefulRequestRowProps {
  request: string;
  isExpanded: boolean;
  onToggle: (id: string) => void;
}

export default function StatefulRequestRow({
  request,
  isExpanded,
  onToggle,
}: StatefulRequestRowProps) {
  const { t } = useTranslation();

  // Fetch execution state
  const { data: state, isLoading: stateLoading, error: stateError } = useExecutionState(request);

  // Fetch activity logs
  const {
    data: events,
    isLoading: eventsLoading,
    error: eventsError,
  } = useQuery({
    queryKey: ['activity-request', request],
    queryFn: () => api.getActivityRequestById(request),
    enabled: isExpanded,
  });

  return (
    <>
      <TableRow onClick={() => onToggle(request)} className="cursor-pointer">
        <TableCell>
          <Span>{request}</Span>
        </TableCell>
        <TableCell>
          {isExpanded ? t('activity.hide_details') : t('activity.show_details')}
        </TableCell>
      </TableRow>
      {isExpanded && (
        <TableRow>
          <TableCell colSpan={3}>
            <div className="grid grid-cols-1 gap-4 py-2 md:grid-cols-2">
              {/* State Section */}
              <div>
                <h3 className="mb-2 font-semibold">{t('state.title')}</h3>
                {stateLoading ? (
                  <div className="flex justify-center p-4">
                    <Spinner size="md" />
                  </div>
                ) : stateError ? (
                  <Panel variant="error" className="my-2">
                    {t('taskstate.error_fetching')}
                  </Panel>
                ) : state && state.state ? (
                  <StateRow state={state.state} />
                ) : (
                  <div className="p-2 text-gray-500">{t('taskstate.no_data')}</div>
                )}
              </div>

              {/* Logs Section */}
              <div>
                <h3 className="mb-2 font-semibold">{t('activity.logs_title')}</h3>
                {eventsLoading ? (
                  <div className="flex justify-center p-4">
                    <Spinner size="md" />
                  </div>
                ) : eventsError ? (
                  <Panel variant="error" className="my-2">
                    {t('activity.error_fetching_events')}
                  </Panel>
                ) : events ? (
                  <EventsRow logs={events} />
                ) : (
                  <div className="p-2 text-gray-500">{t('activity.no_events')}</div>
                )}
              </div>
            </div>
          </TableCell>
        </TableRow>
      )}
    </>
  );
}
