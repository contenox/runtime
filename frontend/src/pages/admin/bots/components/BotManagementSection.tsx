import { GridLayout, Section } from '@contenox/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useBots, useCreateBot, useDeleteBot, useUpdateBot } from '../../../../hooks/useBot';
import { Bot } from '../../../../lib/types';
import BotCard from './BotCard';
import BotForm from './BotForm';

export default function BotManagementSection() {
  const { t } = useTranslation();
  const { data: bots, isLoading, error } = useBots();
  const createMutation = useCreateBot();
  const updateMutation = useUpdateBot();

  const deleteMutation = useDeleteBot();

  const [editingBot, setEditingBot] = useState<Bot | null>(null);
  const [formData, setFormData] = useState<Partial<Bot>>({
    name: '',
    botType: '',
    jobType: '',
    taskChainId: '',
  });

  const resetForm = () => {
    setFormData({
      name: '',
      botType: '',
      jobType: '',
      taskChainId: '',
    });
    setEditingBot(null);
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (editingBot) {
      updateMutation.mutate({ id: editingBot.id, data: formData }, { onSuccess: resetForm });
    } else {
      createMutation.mutate(formData, { onSuccess: resetForm });
    }
  };

  const handleEdit = (bot: Bot) => {
    setEditingBot(bot);
    setFormData({
      name: bot.name,
      botType: bot.botType,
      jobType: bot.jobType,
      taskChainId: bot.taskChainId,
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
            <span>{t('bots.list_loading')}</span>
          </Section>
        )}
        {error && <div className="text-error">{t('bots.list_error')}</div>}
        {bots && bots.length > 0 ? (
          <div>
            {bots.map(bot => (
              <BotCard key={bot.id} bot={bot} onEdit={handleEdit} onDelete={handleDelete} />
            ))}
          </div>
        ) : (
          <Section>{t('bots.list_404')}</Section>
        )}
      </Section>
      <Section>
        <BotForm
          editingBot={editingBot}
          formData={formData}
          setFormData={setFormData}
          onCancel={resetForm}
          onSubmit={handleSubmit}
          isPending={editingBot ? updateMutation.isPending : createMutation.isPending}
          error={createMutation.isError || updateMutation.isError}
        />
      </Section>
    </GridLayout>
  );
}
