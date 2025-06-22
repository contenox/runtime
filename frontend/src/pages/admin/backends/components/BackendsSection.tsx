import { GridLayout, Panel, Section, Span } from '@contenox/ui';
import { t } from 'i18next';
import { useState } from 'react';
import {
  useBackends,
  useCreateBackend,
  useDeleteBackend,
  useUpdateBackend,
} from '../../../../hooks/useBackends';
import { useCreateModel } from '../../../../hooks/useModels';
import { Backend, DownloadStatus } from '../../../../lib/types';
import { BackendCard } from './BackendCard';
import BackendForm from './BackendForm';
import ModelForm from './ModelForm';
import ModelsSection from './ModelSection';

type BackendsSectionProps = {
  statusMap: Record<string, DownloadStatus>;
};

export default function BackendsSection({ statusMap }: BackendsSectionProps) {
  const [newModel, setNewModel] = useState('');

  const createModelMutation = useCreateModel();

  const { data: backends, isLoading, error } = useBackends();
  const createBackendMutation = useCreateBackend();
  const updateBackendMutation = useUpdateBackend();
  const deleteBackendMutation = useDeleteBackend();
  const handleDeclareModel = (e: React.FormEvent) => {
    e.preventDefault();
    if (!newModel.trim()) return;
    createModelMutation.mutate(newModel, {
      onSuccess: () => setNewModel(''),
    });
  };

  const [editingBackend, setEditingBackend] = useState<Backend | null>(null);
  const [name, setName] = useState('');
  const [baseURL, setBaseURL] = useState('');
  const [configType, setConfigType] = useState('ollama');
  const resetForm = () => {
    setName('');
    setBaseURL('');
    setConfigType('ollama');
    setEditingBackend(null);
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (editingBackend) {
      updateBackendMutation.mutate(
        { id: editingBackend.id, data: { name, baseUrl: baseURL, type: configType } },
        { onSuccess: resetForm },
      );
    } else {
      createBackendMutation.mutate(
        { name, baseUrl: baseURL, type: configType },
        { onSuccess: resetForm },
      );
    }
  };

  const handleEdit = (backend: Backend) => {
    setEditingBackend(backend);
    setName(backend.name);
    setBaseURL(backend.baseUrl);
    setConfigType(backend.type);
  };

  const handleDelete = async (id: string) => {
    await deleteBackendMutation.mutateAsync(id);
  };
  console.log(statusMap);

  return (
    <GridLayout variant="body">
      <Section className="overflow-auto">
        {isLoading && (
          <Section className="flex justify-center">
            <Span>{t('backends.list_loading')}</Span>
          </Section>
        )}
        {error && <Panel variant="error">{t('backends.list_error')}</Panel>}
        {backends && backends.length > 0 ? Backends() : <Section>{t('backends.list_404')}</Section>}
        <ModelsSection />
      </Section>
      <Section>
        <BackendForm
          editingBackend={editingBackend}
          onCancel={resetForm}
          onSubmit={handleSubmit}
          isPending={
            editingBackend ? updateBackendMutation.isPending : createBackendMutation.isPending
          }
          error={createBackendMutation.isError || updateBackendMutation.isError}
          name={name}
          setName={setName}
          baseURL={baseURL}
          setBaseURL={setBaseURL}
          configType={configType}
          setConfigType={setConfigType}
        />

        <ModelForm
          newModel={newModel}
          onSubmit={handleDeclareModel}
          onChange={e => setNewModel(e.target.value)}
          isPending={createModelMutation.isPending}
        />
      </Section>
    </GridLayout>
  );
  function Backends() {
    return backends.map(backend => (
      <div key={backend.id}>
        {backends && backends.length > 0 ? (
          <BackendCard
            backend={backend}
            onEdit={handleEdit}
            onDelete={handleDelete}
            statusMap={statusMap}
          />
        ) : (
          <Section>{t('backends.list_404')}</Section>
        )}{' '}
      </div>
    ));
  }
}
