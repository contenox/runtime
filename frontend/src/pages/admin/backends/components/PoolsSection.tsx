import { GridLayout, Panel, Scrollable, Section, Span } from '@cate/ui';
import { t } from 'i18next';
import { useState } from 'react';
import { useCreatePool, useDeletePool, usePools, useUpdatePool } from '../../../../hooks/usePool';
import { Pool } from '../../../../lib/types';
import { PoolCard } from './PoolCard';
import PoolForm from './PoolForm';

export default function PoolsSection() {
  const [editingPool, setEditingPool] = useState<Pool | null>(null);
  const [name, setName] = useState('');
  const [purposeType, setPurposeType] = useState('');

  const { data: pools, isLoading, error } = usePools();
  const createPoolMutation = useCreatePool();
  const updatePoolMutation = useUpdatePool();
  const deletePoolMutation = useDeletePool();

  const resetForm = () => {
    setName('');
    setPurposeType('');
    setEditingPool(null);
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (editingPool) {
      updatePoolMutation.mutate(
        { id: editingPool.id, data: { name, purposeType } },
        { onSuccess: resetForm },
      );
    } else {
      createPoolMutation.mutate({ name, purposeType }, { onSuccess: resetForm });
    }
  };

  const handleEdit = (pool: Pool) => {
    setEditingPool(pool);
    setName(pool.name);
    setPurposeType(pool.purposeType);
  };

  const handleDelete = async (id: string) => {
    await deletePoolMutation.mutateAsync(id);
  };

  return (
    <GridLayout variant="body">
      <Section>
        <Scrollable orientation="vertical">
          {isLoading && (
            <Section className="flex justify-center">
              <Span>{t('pools.list_loading')}</Span>
            </Section>
          )}
          {error && <Panel variant="error">{t('pools.list_error')}</Panel>}
          {pools && pools.length > 0 ? (
            pools.map(pool => (
              <PoolCard
                key={pool.id}
                pool={pool}
                onEdit={handleEdit}
                onDelete={handleDelete}
                isDeleting={deletePoolMutation.isPending}
              />
            ))
          ) : (
            <Section>{t('pools.list_404')}</Section>
          )}
        </Scrollable>
      </Section>
      <Section>
        <PoolForm
          editingPool={editingPool}
          onCancel={resetForm}
          onSubmit={handleSubmit}
          isPending={editingPool ? updatePoolMutation.isPending : createPoolMutation.isPending}
          error={createPoolMutation.isError || updatePoolMutation.isError}
          name={name}
          setName={setName}
          purposeType={purposeType}
          setPurposeType={setPurposeType}
        />
      </Section>
    </GridLayout>
  );
}
