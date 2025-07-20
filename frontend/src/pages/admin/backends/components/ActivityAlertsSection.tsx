import { EmptyState, H3, Panel, Span, Spinner, Table, TableCell, TableRow } from '@contenox/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useActivityAlerts, useActivityRequestById } from '../../../../hooks/useActitivy';
import { Alert } from '../../../../lib/types';
import EventsRow from './EventsRow';

export default function ActivityAlertsSection() {
  const { t } = useTranslation();
  const { data: alerts, isLoading, isError, error } = useActivityAlerts(100);
  const [expandedAlert, setExpandedAlert] = useState<string | null>(null);

  const toggleAlert = (alertId: string) => {
    setExpandedAlert(expandedAlert === alertId ? null : alertId);
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
        {t('activity.error_fetching_alerts')}: {error.message}
      </Panel>
    );
  }

  if (!alerts || alerts.length === 0) {
    return (
      <EmptyState
        title={t('activity.alerts_empty_title')}
        description={t('activity.alerts_empty_description')}
      />
    );
  }

  return (
    <div className="overflow-auto">
      <Table columns={[t('activity.message'), t('activity.severity'), t('activity.timestamp')]}>
        {alerts.map(alert => (
          <AlertRow
            key={alert.id}
            alert={alert}
            isExpanded={expandedAlert === alert.id}
            onToggle={toggleAlert}
          />
        ))}
      </Table>
    </div>
  );
}

interface AlertRowProps {
  alert: Alert;
  isExpanded: boolean;
  onToggle: (id: string) => void;
}

function AlertRow({ alert, isExpanded, onToggle }: AlertRowProps) {
  const { t } = useTranslation();
  const {
    data: events,
    isLoading,
    isError,
  } = useActivityRequestById(alert.requestID, {
    enabled: isExpanded && !!alert.requestID,
  });

  return (
    <>
      <TableRow onClick={() => onToggle(alert.id)} className="cursor-pointer">
        <TableCell>
          <Span>{alert.message}</Span>
        </TableCell>
        <TableCell>
          <Span>{new Date(alert.timestamp).toLocaleString()}</Span>
        </TableCell>
      </TableRow>
      {isExpanded && (
        <TableRow>
          <TableCell colSpan={3}>
            <div className="grid grid-cols-1 gap-4 py-2 md:grid-cols-2">
              {/* Metadata Section */}
              <div>
                <H3 className="mb-2">{t('activity.metadata')}</H3>
                <Panel>
                  <pre className="text-xs">{JSON.stringify(alert.metadata, null, 2)}</pre>
                </Panel>
              </div>

              {/* Request Logs Section */}
              <Panel>
                <H3 className="mb-2">{t('activity.logs_title')}</H3>
                {isLoading ? (
                  <div className="flex justify-center p-4">
                    <Spinner size="md" />
                  </div>
                ) : isError ? (
                  <Panel variant="error" className="my-2">
                    {t('activity.error_fetching_request_events')}
                  </Panel>
                ) : events && events.length > 0 ? (
                  <EventsRow logs={events} />
                ) : (
                  <div className="p-2">{t('activity.no_events_for_request')}</div>
                )}
              </Panel>
            </div>
          </TableCell>
        </TableRow>
      )}
    </>
  );
}
