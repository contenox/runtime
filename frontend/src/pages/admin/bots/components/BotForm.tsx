import { Button, Form, FormField, Input, Select } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import { Bot } from '../../../../lib/types';

type BotFormProps = {
  editingBot: Bot | null;
  formData: Partial<Bot>;
  setFormData: (data: Partial<Bot>) => void;
  onCancel: () => void;
  onSubmit: (e: React.FormEvent) => void;
  isPending: boolean;
  error: boolean;
};

export default function BotForm({
  editingBot,
  formData,
  setFormData,
  onCancel,
  onSubmit,
  isPending,
  error,
}: BotFormProps) {
  const { t } = useTranslation();

  const handleChange = (field: keyof Bot, value: string | number) => {
    setFormData({ ...formData, [field]: value });
  };

  const botTypes = ['github', 'telegram', 'system', 'custom'];
  const jobTypes = ['comment_processor', 'message_handler', 'event_collector', 'custom'];

  return (
    <Form
      title={editingBot ? t('bots.form_title_edit') : t('bots.form_title_create')}
      onSubmit={onSubmit}
      error={error ? t(editingBot ? 'bots.update_error' : 'bots.create_error') : undefined}
      actions={
        <>
          <Button type="submit" variant="primary" disabled={isPending}>
            {editingBot
              ? isPending
                ? t('common.updating')
                : t('bots.form_update_action')
              : isPending
                ? t('common.creating')
                : t('bots.form_create_action')}
          </Button>
          {editingBot && (
            <Button type="button" variant="secondary" onClick={onCancel}>
              {t('common.cancel')}
            </Button>
          )}
        </>
      }>
      <FormField label={t('bots.name')} required>
        <Input
          value={formData.name || ''}
          onChange={e => handleChange('name', e.target.value)}
          placeholder={t('bots.name_placeholder')}
        />
      </FormField>

      <FormField label={t('bots.type')} required>
        <Select
          value={formData.botType || ''}
          onChange={e => handleChange('botType', e.target.value)}
          placeholder={t('bots.type_placeholder')}
          options={[{ value: 'GitHub', label: 'GitHub' }]}>
          {botTypes.map(type => (
            <option key={type} value={type}>
              {type}
            </option>
          ))}
        </Select>
      </FormField>

      <FormField label={t('bots.job_type')} required>
        <Select
          value={formData.jobType || ''}
          onChange={e => handleChange('jobType', e.target.value)}
          placeholder={t('bots.job_type_placeholder')}
          options={[{ value: 'github_process_comment_llm', label: 'GitHub' }]}>
          {jobTypes.map(type => (
            <option key={type} value={type}>
              {type}
            </option>
          ))}
        </Select>
      </FormField>

      <FormField label={t('bots.task_chain_id')} required>
        <Input
          value={formData.taskChainId || ''}
          onChange={e => handleChange('taskChainId', e.target.value)}
          placeholder={t('bots.task_chain_id_placeholder')}
        />
      </FormField>
    </Form>
  );
}
