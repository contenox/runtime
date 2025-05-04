import { Button, ButtonGroup, Label, P, Panel, Section, Select, Spinner } from '@cate/ui';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  useAssignBackendToPool,
  usePools,
  usePoolsForBackend,
  useRemoveBackendFromPool,
} from '../../../../hooks/usePool';
import { Backend, DownloadStatus, Pool } from '../../../../lib/types';
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
  const [errorMessage, setErrorMessage] = useState('');
  const [successMessage, setSuccessMessage] = useState('');

  const { data: pools } = usePools();
  const { data: backendPools = [] } = usePoolsForBackend(backend.id);
  const assignMutation = useAssignBackendToPool();
  const removeMutation = useRemoveBackendFromPool();

  const [selectedPoolToAssign, setSelectedPoolToAssign] = useState('');

  useEffect(() => {
    if (errorMessage) {
      const timer = setTimeout(() => setErrorMessage(''), 5000);
      return () => clearTimeout(timer);
    }
  }, [errorMessage]);

  useEffect(() => {
    if (successMessage) {
      const timer = setTimeout(() => setSuccessMessage(''), 5000);
      return () => clearTimeout(timer);
    }
  }, [successMessage]);

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
    removeMutation.mutate(
      { poolID, backendID: backend.id },
      {
        onSuccess: () => {
          setSuccessMessage(t('backends.pool_removed_success'));
        },
        onError: error => {
          setErrorMessage(error.message || t('backends.pool_remove_error'));
        },
      },
    );
  };

  const handleAssignPool = (poolId: string) => {
    setSelectedPoolToAssign(poolId);
    assignMutation.mutate(
      { poolID: poolId, backendID: backend.id },
      {
        onSuccess: () => {
          setSelectedPoolToAssign('');
          setSuccessMessage(t('backends.pool_assigned_success'));
        },
        onError: error => {
          console.error('Assign mutation failed:', error);
          setSelectedPoolToAssign('');
          setErrorMessage(error.message || t('backends.pool_assign_error'));
        },
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

  const poolOptions = pools?.map((pool: Pool) => ({ value: pool.id, label: pool.name })) || [];

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
          isPulled={backend.pulledModels.some(pulledModel => pulledModel.name === model)}
        />
      ))}

      <div className="mt-4">
        <Label className="block text-sm font-medium">{t('backends.assigned_pools')}</Label>
        {backendPools?.length > 0 ? (
          <ul className="list-inside list-disc pl-2">
            {backendPools.map((pool: Pool) => (
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

      {errorMessage && (
        <Panel variant="error" className="mt-4">
          {errorMessage}
        </Panel>
      )}

      {successMessage && (
        <Panel variant="flat" className="mt-4">
          {successMessage}
        </Panel>
      )}

      <div className="flex items-center gap-2 border-t pt-4">
        <Label htmlFor={`assign-${backend.id}`} className="text-sm font-medium">
          {t('backends.assign_to_pool')}
        </Label>
        <Select
          id={`assign-${backend.id}`}
          className="flex-grow rounded border px-2 py-1 text-sm"
          value={selectedPoolToAssign}
          onChange={e => {
            handleAssignPool(e.target.value);
          }}
          disabled={assignMutation.isPending || !poolOptions.length}
          placeholder={t('backends.select_pool')}
          options={poolOptions}
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
