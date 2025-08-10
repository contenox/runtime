import { P, Panel } from '@contenox/ui';
import { t } from 'i18next';
import { useEffect, useState } from 'react';
import { useDeleteModel, useModels, useUpdateModel } from '../../../../hooks/useModels';
import { Model } from '../../../../lib/types';
import { ModelCard } from './ModelCard';
import ModelForm from './ModelForm';

export default function ModelsSection() {
  const { data: models, isLoading, error } = useModels();
  const deleteModelMutation = useDeleteModel();
  const updateModelMutation = useUpdateModel();

  const [deletingModel, setDeletingModel] = useState<string | null>(null);
  const [editingModel, setEditingModel] = useState<Model | null>(null);
  const [formData, setFormData] = useState<Partial<Model>>({});

  useEffect(() => {
    if (editingModel) {
      setFormData(editingModel);
    }
  }, [editingModel]);

  const handleDeleteModel = (id: string) => {
    setDeletingModel(id);
    deleteModelMutation.mutate(id, {
      onSettled: () => setDeletingModel(null),
    });
  };

  const handleEditModel = (model: Model) => {
    setEditingModel(model);
  };

  const handleUpdateModel = (id: string, data: Partial<Model>) => {
    updateModelMutation.mutate(
      { id, data },
      {
        onSuccess: () => setEditingModel(null),
      },
    );
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
        <p>{t('model.list_error') + error}</p>
      </Panel>
    );
  }

  return (
    <>
      {models?.map(model =>
        editingModel?.id === model.id ? (
          <ModelForm
            key={`edit-${model.id}`}
            editingModel={editingModel}
            formData={formData}
            setFormData={setFormData}
            onCancel={() => setEditingModel(null)}
            onSubmit={e => {
              e.preventDefault();
              handleUpdateModel(model.id, formData);
            }}
            isPending={updateModelMutation.isPending}
            error={updateModelMutation.isError}
          />
        ) : (
          <ModelCard
            key={model.id}
            model={model}
            onDelete={handleDeleteModel}
            onEdit={handleEditModel}
            deletePending={deletingModel === model.id}
          />
        ),
      )}
    </>
  );
}
