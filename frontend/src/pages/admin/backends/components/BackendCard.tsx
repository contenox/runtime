import { Button, ButtonGroup, P, Section, Select, Spinner } from '@cate/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  useAssignBackendToPool,
  usePools,
  usePoolsForBackend,
  useRemoveBackendFromPool,
} from '../../../../hooks/usePool';
import { Backend, DownloadStatus } from '../../../../lib/types';
import { ModelStatusDisplay } from './ModelStatusDisplay';

type BackendCardProps = {
  backend: Backend;
  onEdit: (backend: Backend) => void;
  onDelete: (id: string) => Promise<void>;
  statusMap: Record<string, DownloadStatus>;
};

export function BackendCard({ backend, onEdit, onDelete, statusMap }: BackendCardProps) {
  const { t } = useTranslation();
  const [deletingBackendId, setDeletingBackendId] = useState<string | null>(null);

  const { data: pools } = usePools();
  const { data: backendPools } = usePoolsForBackend(backend.id);
  const assignMutation = useAssignBackendToPool();
  const removeMutation = useRemoveBackendFromPool();

  const [selectedPoolToAssign, setSelectedPoolToAssign] = useState('');

  const isRemovingPool = (poolId: string) =>
    removeMutation.isPending && removeMutation.variables?.poolID === poolId;

  const handleDelete = async (id: string) => {
    setDeletingBackendId(id);
    try {
      await onDelete(id);
    } finally {
      setDeletingBackendId(null);
    }
  };

  const handleRemovePool = (poolID: string) => {
    removeMutation.mutate({ poolID, backendID: backend.id });
  };

  const handleAssignPool = (poolId: string) => {
    if (!poolId) return;

    setSelectedPoolToAssign(poolId);
    assignMutation.mutate(
      { poolID: poolId, backendID: backend.id },
      {
        onSuccess: () => setSelectedPoolToAssign(''),
        onError: () => setSelectedPoolToAssign(''),
      },
    );
  };

  const getDownloadStatusForModel = (
    statusMap: Record<string, DownloadStatus>,
    baseUrl: string,
    modelName: string,
  ): DownloadStatus | undefined => {
    const key = `${baseUrl}:${modelName}`;
    return statusMap[key];
  };

  return (
    <Section title={backend.name} key={backend.id}>
      <P>{backend.baseUrl}</P>
      <P>
        {t('common.type')} {backend.type}
      </P>

      {backend.models?.map(model => (
        <ModelStatusDisplay
          key={model}
          modelName={model}
          downloadStatus={getDownloadStatusForModel(statusMap, backend.baseUrl, model)}
          isPulled={false}
        />
      ))}

      <div className="mt-4">
        <label className="block text-sm font-medium">{t('backends.assigned_pools')}</label>
        {backendPools && backendPools.length > 0 ? (
          <ul className="list-inside list-disc pl-2">
            {backendPools.map(pool => (
              <li key={pool.id} className="flex items-center justify-between py-1">
                <span>{pool.name}</span>
                <Button
                  variant="ghost"
                  onClick={() => handleRemovePool(pool.id)}
                  disabled={isRemovingPool(pool.id)}
                  className="text-error">
                  {isRemovingPool(pool.id) ? <Spinner size="sm" /> : t('common.remove')}
                </Button>
              </li>
            ))}
          </ul>
        ) : (
          <P variant="muted">{t('backends.not_assigned_to_any_pools')}</P>
        )}
      </div>

      <div className="flex items-center gap-2 border-t pt-4">
        <label htmlFor={`assign-${backend.id}`} className="text-sm font-medium">
          {t('backends.assign_to_pool')}
        </label>
        <Select
          id={`assign-${backend.id}`}
          className="flex-grow rounded border px-2 py-1 text-sm"
          value={selectedPoolToAssign}
          onChange={e => handleAssignPool(e.target.value)}
          disabled={assignMutation.isPending || !pools?.length}
          defaultValue={t('backends.select_pool')}
          options={pools.map(pool => ({ value: pool.id, label: pool.name }))}
        />
        {assignMutation.isPending && <Spinner size="sm" />}
      </div>

      <ButtonGroup className="mt-4">
        <Button variant="ghost" size="sm" onClick={() => onEdit(backend)}>
          {t('common.edit')}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => handleDelete(backend.id)}
          disabled={deletingBackendId === backend.id}>
          {deletingBackendId === backend.id ? t('common.deleting') : t('common.delete')}
        </Button>
      </ButtonGroup>
    </Section>
  );
}
