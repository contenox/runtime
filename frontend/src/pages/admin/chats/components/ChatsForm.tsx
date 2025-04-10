import { Button, Form, FormField, Select } from '@cate/ui';
import { t } from 'i18next';

type ChatsFormProps = {
  onSubmit: (e: React.FormEvent) => void;
  isPending: boolean;
  onCancel: () => void;
  models: string[];
  error?: string;
  selectedModel: string;
  setSelectedModel: (value: string) => void;
};

export function ChatsForm({
  onSubmit,
  isPending,
  onCancel,
  error,
  models,
  selectedModel,
  setSelectedModel,
}: ChatsFormProps) {
  const options: Array<{ value: string; label: string }> = models.map(model => ({
    value: model,
    label: model,
  }));

  return (
    <Form
      onSubmit={onSubmit}
      title={t('chat.start_new_chat')}
      error={error}
      onError={errorMsg => console.error('Form error:', errorMsg)}
      actions={
        <>
          <Button type="submit" variant="primary" disabled={isPending}>
            {isPending ? t('common.creating') : t('chat.create_chat')}
          </Button>
          <Button type="button" variant="secondary" onClick={onCancel}>
            {t('common.cancel')}
          </Button>
        </>
      }>
      <FormField label={t('chat.select_model')} required>
        <Select
          value={selectedModel}
          onChange={e => setSelectedModel(e.target.value)}
          options={options}
        />
      </FormField>
    </Form>
  );
}
