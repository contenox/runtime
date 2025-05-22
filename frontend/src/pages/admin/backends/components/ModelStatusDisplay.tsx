import { P, Span } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import { DownloadStatus } from '../../../../lib/types';

type ModelStatusDisplayProps = {
  modelName: string;
  downloadStatus?: DownloadStatus;
  isPulled: boolean;
};

export function ModelStatusDisplay({
  modelName,
  downloadStatus,
  isPulled,
}: ModelStatusDisplayProps) {
  const { t } = useTranslation();

  let statusElement: React.ReactNode;

  if (downloadStatus) {
    const percentage = (
      ((downloadStatus.completed || 0) / (downloadStatus.total || 1)) *
      100
    ).toFixed(0);
    const statusKey = `backends.status.${downloadStatus.status}`;
    const statusText = t(statusKey, downloadStatus.status);
    statusElement = (
      <Span>
        {statusText} ({percentage}%)
      </Span>
    );
  } else if (isPulled) {
    statusElement = <Span>{t('backends.status.downloaded', 'Downloaded')}</Span>;
  } else {
    statusElement = <Span>{t('backends.status.not_downloaded', 'Not Downloaded')}</Span>;
  }

  return (
    <P key={modelName}>
      {' '}
      <Span className="font-semibold">{modelName}:</Span> {statusElement}
    </P>
  );
}
