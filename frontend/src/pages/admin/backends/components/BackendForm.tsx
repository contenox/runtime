import { Button, Form, FormField, Input, Select } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import { Backend } from '../../../../lib/types';

type BackendFormProps = {
  editingBackend: Backend | null;
  onCancel: () => void;
  onSubmit: (e: React.FormEvent) => void;
  isPending: boolean;
  error: boolean;
  name: string;
  setName: (value: string) => void;
  baseURL: string;
  setBaseURL: (value: string) => void;
  configType: string;
  setConfigType: (value: string) => void;
};
export default function BackendForm({
  editingBackend,
  onCancel,
  onSubmit,
  isPending,
  error,
  name,
  setName,
  baseURL,
  setBaseURL,
  configType,
  setConfigType,
}: BackendFormProps) {
  const { t } = useTranslation();
  return (
    <Form
      title={editingBackend ? t('backends.form_title_edit') : t('backends.form_title_create')}
      onSubmit={onSubmit}
      error={
        error
          ? `Error ${editingBackend ? 'updating' : 'creating'} backend. Please try again.`
          : undefined
      }
      onError={errorMsg => console.error('Form error:', errorMsg)}
      actions={
        <>
          <Button type="submit" variant="primary" disabled={isPending}>
            {editingBackend
              ? isPending
                ? t('common.updating')
                : t('backends.form_update_action')
              : isPending
                ? t('common.creating')
                : t('backends.form_create_action')}
          </Button>
          {editingBackend && (
            <Button type="button" variant="secondary" onClick={onCancel}>
              {t('common.cancel')}
            </Button>
          )}
        </>
      }>
      <FormField label={t('common.name')} required>
        <Input value={name} onChange={e => setName(e.target.value)} />
      </FormField>

      <FormField label={t('backends.form_url')} required>
        <Input value={baseURL} onChange={e => setBaseURL(e.target.value)} />
      </FormField>

      <FormField label={t('backends.form_type')} required>
        <Select
          value={configType}
          onChange={e => setConfigType(e.target.value)}
          options={[
            { value: 'ollama', label: 'Ollama' },
            { value: 'vllm', label: 'vLLM' },
          ]}
        />
      </FormField>
    </Form>
  );
}
