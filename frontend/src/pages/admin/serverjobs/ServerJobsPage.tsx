import { EmptyState, Panel, Section, Spinner, Table, TableCell, TableRow } from '@cate/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useInProgressJobs, usePendingJobs } from '../../../hooks/useServerJobs';
import { InProgressJob, PendingJob } from '../../../lib/types';

export default function ServerJobsPage() {
  const { t } = useTranslation();
  const [cursor] = useState<Date>(); // TODO: setCursor pagination
  const {
    data: pendingJobs,
    isLoading: pendingLoading,
    error: pendingError,
  } = usePendingJobs(cursor);

  const {
    data: inProgressJobs,
    isLoading: inProgressLoading,
    error: inProgressError,
  } = useInProgressJobs(cursor);

  const formatTimestamp = (timestamp: number) => new Date(timestamp).toLocaleString();

  const renderJobTable = (jobs: Array<PendingJob | InProgressJob>, isInProgress = false) => (
    <Table
      columns={[
        t('serverJobs.taskType'),
        t('serverJobs.scheduledFor'),
        t('serverJobs.validUntil'),
        t('serverJobs.retries'),
        ...(isInProgress ? [t('serverJobs.leaser'), t('serverJobs.leaseExpiration')] : []),
        t('serverJobs.createdAt'),
      ]}>
      {jobs.map(job => (
        <TableRow key={job.id}>
          <TableCell>{job.taskType}</TableCell>
          <TableCell>{formatTimestamp(job.scheduledFor)}</TableCell>
          <TableCell>{formatTimestamp(job.validUntil)}</TableCell>
          <TableCell>{job.retryCount}</TableCell>
          {isInProgress && (
            <>
              <TableCell>{(job as InProgressJob).leaser}</TableCell>
              <TableCell>
                {formatTimestamp(Date.parse((job as InProgressJob).leaseExpiration))}
              </TableCell>
            </>
          )}
          <TableCell>{formatTimestamp(Date.parse(job.createdAt))}</TableCell>
        </TableRow>
      ))}
    </Table>
  );

  return (
    <Section title={t('serverJobs.title')}>
      <div className="space-y-6">
        {/* Pending Jobs Panel */}
        <Panel title={t('serverJobs.pendingTitle')}>
          {pendingLoading ? (
            <div className="flex justify-center p-4">
              <Spinner size="lg" />
            </div>
          ) : pendingError ? (
            <Panel variant="error" title={t('serverJobs.errorTitle')}>
              {pendingError.message}
            </Panel>
          ) : pendingJobs?.length ? (
            renderJobTable(pendingJobs)
          ) : (
            <EmptyState
              title={t('serverJobs.noPendingJobs')}
              description={t('serverJobs.noPendingJobsDescription')}
            />
          )}
        </Panel>

        <Panel title={t('serverJobs.inProgressTitle')}>
          {inProgressLoading ? (
            <div className="flex justify-center p-4">
              <Spinner size="lg" />
            </div>
          ) : inProgressError ? (
            <Panel variant="error" title={t('serverJobs.errorTitle')}>
              {inProgressError.message}
            </Panel>
          ) : inProgressJobs?.length ? (
            renderJobTable(inProgressJobs, true)
          ) : (
            <EmptyState
              title={t('serverJobs.noInProgressJobs')}
              description={t('serverJobs.noInProgressJobsDescription')}
            />
          )}
        </Panel>
      </div>
    </Section>
  );
}
