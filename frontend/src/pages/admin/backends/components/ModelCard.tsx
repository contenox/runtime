import { Button, Label, P, Panel, Section, Select, Spinner } from '@contenox/ui';
import { t } from 'i18next';
import { useEffect, useState } from 'react';
import {
  useAssignModelToPool,
  usePools,
  usePoolsForModel,
  useRemoveModelFromPool,
} from '../../../../hooks/usePool';
import { OpenAIModel } from '../../../../lib/types';

type ModelCardProps = {
  model: OpenAIModel;
  onDelete: (modelId: string) => void;
  deletePending: boolean;
};

export function ModelCard({ model, onDelete, deletePending }: ModelCardProps) {
  const [selectedPoolToAssign, setSelectedPoolToAssign] = useState('');
  const [errorMessage, setErrorMessage] = useState('');
  const [successMessage, setSuccessMessage] = useState('');

  const { data: allPools } = usePools();
  const { data: associatedPools, isLoading: isLoadingAssociated } = usePoolsForModel(model.id);
  const assignMutation = useAssignModelToPool();
  const removeMutation = useRemoveModelFromPool();

  const poolOptions = allPools?.map(pool => ({ value: pool.id, label: pool.name })) || [];

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

  const handleAssign = (poolId: string) => {
    setSelectedPoolToAssign(poolId);
    assignMutation.mutate(
      { poolID: poolId, modelID: model.id },
      {
        onSuccess: () => {
          setSelectedPoolToAssign('');
          setSuccessMessage(t('model.pool_assigned_success'));
        },
        onError: error => {
          console.error('Assign mutation failed:', error);
          setSelectedPoolToAssign('');
          setErrorMessage(error.message || t('model.pool_assign_error'));
        },
      },
    );
  };

  const handleRemove = (poolID: string) => {
    removeMutation.mutate(
      { poolID, modelID: model.id },
      {
        onSuccess: () => setSuccessMessage(t('model.pool_removed_success')),
        onError: error => setErrorMessage(error.message || t('model.pool_remove_error')),
      },
    );
  };

  const isRemovingPool = (poolId: string) =>
    removeMutation.isPending && removeMutation.variables?.poolID === poolId;

  return (
    <Section key={model.id} title={model.id}>
      <div className="flex justify-between">
        <div>
          {model.created && (
            <P>
              <small>
                {t('common.created_at')} {new Date(model.created).toLocaleString()}
              </small>
            </P>
          )}
        </div>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => onDelete(model.id)}
          className="text-error"
          disabled={deletePending}>
          {deletePending ? t('common.deleting') : t('translation:model.model_delete')}
        </Button>
      </div>

      {errorMessage && (
        <Panel variant="error" className="mt-2">
          {errorMessage}
        </Panel>
      )}

      {successMessage && (
        <Panel variant="flat" className="mt-2">
          {successMessage}
        </Panel>
      )}

      <div>
        <P>{t('model.assigned_pools')}</P>
        {isLoadingAssociated ? (
          <Spinner size="sm" />
        ) : associatedPools && associatedPools.length > 0 ? (
          <ul className="list-inside list-disc pl-2">
            {associatedPools.map(pool => (
              <li key={pool.id} className="flex items-center justify-between py-1">
                <span>{pool.name}</span>
                <Button
                  variant="ghost"
                  onClick={() => handleRemove(pool.id)}
                  disabled={isRemovingPool(pool.id)}
                  className="text-error">
                  {isRemovingPool(pool.id) ? <Spinner size="sm" /> : t('common.remove')}
                </Button>
              </li>
            ))}
          </ul>
        ) : (
          <P variant="muted">{t('model.not_assigned_to_any_pools')}</P>
        )}
      </div>

      <div className="flex items-center gap-2 border-t pt-4">
        <Label htmlFor={`assign-${model.id}`} className="text-sm font-medium">
          {t('model.assign_to_pool')}
        </Label>
        <Select
          id={`assign-${model.id}`}
          className="flex-grow rounded border px-2 py-1 text-sm"
          value={selectedPoolToAssign}
          onChange={e => handleAssign(e.target.value)}
          placeholder={t('model.select_pool_to_assign')}
          disabled={assignMutation.isPending || poolOptions.length === 0}
          options={poolOptions}
        />
        {assignMutation.isPending && <Spinner size="sm" />}
      </div>
    </Section>
  );
}
