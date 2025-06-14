import { P, Panel } from '@contenox/ui';
import { t } from 'i18next';
import { useState } from 'react';
import { useDeleteModel, useModels } from '../../../../hooks/useModels';
import { ModelCard } from './ModelCard';
export default function ModelsSection() {
  const { data: models, isLoading, error } = useModels();
  const deleteModelMutation = useDeleteModel();

  // State to track which model is currently being deleted
  const [deletingModel, setDeletingModel] = useState<string | null>(null);

  const handleDeleteModel = (model: string) => {
    setDeletingModel(model);
    deleteModelMutation.mutate(model, {
      onSettled: () => setDeletingModel(null),
    });
  };

  if (isLoading) {
    return (
      <Panel className="p-4">
        <P>{t('model.list_loading')}</P>
      </Panel>
    );
  }

  if (error) {
    return (
      <Panel variant="error" className="p-4">
        <p>{t('model.list_error')}</p>
      </Panel>
    );
  }

  return (
    <>
      {models.data.map(model => (
        <ModelCard
          key={model.id}
          model={model}
          onDelete={handleDeleteModel}
          deletePending={deletingModel === model.id}
        />
      ))}
    </>
  );
}
