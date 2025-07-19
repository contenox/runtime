import { Button, Form, FormField, Input, Select } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import { TelegramFrontend } from '../../../../lib/types';

type TelegramFormProps = {
  editingFrontend: TelegramFrontend | null;
  formData: Partial<TelegramFrontend>;
  setFormData: (data: Partial<TelegramFrontend>) => void;
  onCancel: () => void;
  onSubmit: (e: React.FormEvent) => void;
  isPending: boolean;
  error: boolean;
};

export default function TelegramForm({
  editingFrontend,
  formData,
  setFormData,
  onCancel,
  onSubmit,
  isPending,
  error,
}: TelegramFormProps) {
  const { t } = useTranslation();

  const handleChange = (field: keyof TelegramFrontend, value: string | number) => {
    setFormData({ ...formData, [field]: value });
  };

  return (
    <Form
      title={editingFrontend ? t('telegram.form_title_edit') : t('telegram.form_title_create')}
      onSubmit={onSubmit}
      error={
        error ? t(editingFrontend ? 'telegram.update_error' : 'telegram.create_error') : undefined
      }
      actions={
        <>
          <Button type="submit" variant="primary" disabled={isPending}>
            {editingFrontend
              ? isPending
                ? t('common.updating')
                : t('telegram.form_update_action')
              : isPending
                ? t('common.creating')
                : t('telegram.form_create_action')}
          </Button>
          {editingFrontend && (
            <Button type="button" variant="secondary" onClick={onCancel}>
              {t('common.cancel')}
            </Button>
          )}
        </>
      }>
      <FormField label={t('telegram.bot_token')} required>
        <Input
          value={formData.botToken || ''}
          onChange={e => handleChange('botToken', e.target.value)}
          placeholder="123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11"
        />
      </FormField>

      <FormField label={t('telegram.user_id')} required>
        <Input
          value={formData.userId || ''}
          onChange={e => handleChange('userId', e.target.value)}
          placeholder="123456789"
        />
      </FormField>

      <FormField label={t('telegram.description')}>
        <Input
          value={formData.description || ''}
          onChange={e => handleChange('description', e.target.value)}
        />
      </FormField>

      <FormField label={t('telegram.sync_interval')}>
        <Input
          type="number"
          value={formData.syncInterval || 60}
          onChange={e => handleChange('syncInterval', parseInt(e.target.value))}
        />
      </FormField>

      <FormField label={t('telegram.status')}>
        <Select
          value={formData.status || 'active'}
          onChange={e => handleChange('status', e.target.value)}
          options={[
            { value: 'active', label: t('common.active') },
            { value: 'inactive', label: t('common.inactive') },
            { value: 'error', label: t('common.error') },
          ]}
        />
      </FormField>

      <FormField label={t('telegram.chat_chain')}>
        <Input
          value={formData.chatChain || ''}
          onChange={e => handleChange('chatChain', e.target.value)}
          placeholder="default-chain"
        />
      </FormField>
    </Form>
  );
}
