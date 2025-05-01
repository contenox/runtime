import { Button, P, Section, Spinner } from '@cate/ui';
import { t } from 'i18next';
import { useState } from 'react';
import {
  useAssignModelToPool,
  usePools,
  usePoolsForModel,
  useRemoveModelFromPool,
} from '../../../../hooks/usePool';
import { Model } from '../../../../lib/types';

type ModelCardProps = {
  model: Model;
  onDelete: (modelId: string) => void;
  deletePending: boolean;
};

export function ModelCard({ model, onDelete, deletePending }: ModelCardProps) {
  const [selectedPoolToAssign, setSelectedPoolToAssign] = useState('');

  const { data: allPools } = usePools();

  const { data: associatedPools, isLoading: isLoadingAssociated } = usePoolsForModel(model.id);

  const assignMutation = useAssignModelToPool();
  const removeMutation = useRemoveModelFromPool();

  const handleAssign = (poolID: string) => {
    if (!poolID) return;

    setSelectedPoolToAssign(poolID);
    assignMutation.mutate(
      { poolID, modelID: model.id },
      {
        onSuccess: () => {
          setSelectedPoolToAssign('');
        },
      },
    );
  };

  const handleRemove = (poolID: string) => {
    removeMutation.mutate({ poolID, modelID: model.id });
  };

  const isRemovingPool = (poolId: string) =>
    removeMutation.isPending && removeMutation.variables?.poolID === poolId;

  return (
    <Section key={model.id} title={model.model}>
      <div className="flex justify-between">
        <div>
          {model.createdAt && (
            <P>
              <small>
                {t('common.created_at')} {new Date(model.createdAt).toLocaleString()}
              </small>
            </P>
          )}
          {model.updatedAt && (
            <P>
              <small>
                {t('common.updated_at')} {new Date(model.updatedAt).toLocaleString()}
              </small>
            </P>
          )}
        </div>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => onDelete(model.id)} // Pass ID
          className="text-error"
          disabled={deletePending}>
          {deletePending ? t('common.deleting') : t('translation:model.model_delete')}
        </Button>
      </div>
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
        <label htmlFor={`assign-${model.id}`} className="text-sm font-medium">
          {t('model.assign_to_pool')}
        </label>
        <select
          id={`assign-${model.id}`}
          className="flex-grow rounded border px-2 py-1 text-sm"
          value={selectedPoolToAssign}
          onChange={e => handleAssign(e.target.value)}
          disabled={assignMutation.isPending || allPools.length === 0}>
          <option value="">{t('model.select_pool_to_assign')}</option>
          {allPools.map(pool => (
            <option key={pool.id} value={pool.id}>
              {pool.name}
            </option>
          ))}
        </select>
        {assignMutation.isPending && <Spinner size="sm" />}
      </div>
    </Section>
  );
}
