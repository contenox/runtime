import { GridLayout, Section } from '@contenox/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  useCreateTelegramFrontend,
  useDeleteTelegramFrontend,
  useTelegramFrontends,
  useUpdateTelegramFrontend,
} from '../../../../hooks/useTelegramFrontends';
import { TelegramFrontend } from '../../../../lib/types';
import TelegramCard from './TelegramCard';
import TelegramForm from './TelegramForm';

export default function TelegramFrontendsSection() {
  const { t } = useTranslation();
  const { data: frontends, isLoading, error } = useTelegramFrontends();
  const createMutation = useCreateTelegramFrontend();
  const updateMutation = useUpdateTelegramFrontend();
  const deleteMutation = useDeleteTelegramFrontend();

  const [editingFrontend, setEditingFrontend] = useState<TelegramFrontend | null>(null);
  const [formData, setFormData] = useState<Partial<TelegramFrontend>>({
    botToken: '',
    userId: '',
    description: '',
    syncInterval: 60,
    status: 'active',
  });

  const resetForm = () => {
    setFormData({
      botToken: '',
      userId: '',
      description: '',
      syncInterval: 60,
      status: 'active',
    });
    setEditingFrontend(null);
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();

    if (editingFrontend) {
      updateMutation.mutate({ id: editingFrontend.id, data: formData }, { onSuccess: resetForm });
    } else {
      createMutation.mutate(formData, { onSuccess: resetForm });
    }
  };

  const handleEdit = (frontend: TelegramFrontend) => {
    setEditingFrontend(frontend);
    setFormData({
      botToken: frontend.botToken,
      userId: frontend.userId,
      description: frontend.description,
      syncInterval: frontend.syncInterval,
      status: frontend.status,
      chatChain: frontend.chatChain,
    });
  };

  const handleDelete = async (id: string) => {
    await deleteMutation.mutateAsync(id);
  };

  return (
    <GridLayout variant="body">
      <Section className="overflow-auto">
        {isLoading && (
          <Section className="flex justify-center">
            <span>{t('telegram.list_loading')}</span>
          </Section>
        )}
        {error && <div className="text-error">{t('telegram.list_error')}</div>}

        {frontends && frontends.length > 0 ? (
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
            {frontends.map(frontend => (
              <TelegramCard
                key={frontend.id}
                frontend={frontend}
                onEdit={handleEdit}
                onDelete={handleDelete}
              />
            ))}
          </div>
        ) : (
          <Section>{t('telegram.list_404')}</Section>
        )}
      </Section>

      <Section>
        <TelegramForm
          editingFrontend={editingFrontend}
          formData={formData}
          setFormData={setFormData}
          onCancel={resetForm}
          onSubmit={handleSubmit}
          isPending={editingFrontend ? updateMutation.isPending : createMutation.isPending}
          error={createMutation.isError || updateMutation.isError}
        />
      </Section>
    </GridLayout>
  );
}
