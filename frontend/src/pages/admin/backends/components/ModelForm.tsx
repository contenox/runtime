import { Button, Checkbox, Form, FormField, Input, Section } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import { Model } from '../../../../lib/types';

type ModelFormProps = {
  editingModel: Model | null;
  formData: Partial<Model>;
  setFormData: (data: Partial<Model>) => void;
  onCancel: () => void;
  onSubmit: (e: React.FormEvent) => void;
  isPending: boolean;
  error: boolean;
};

export default function ModelForm({
  editingModel,
  formData,
  setFormData,
  onCancel,
  onSubmit,
  isPending,
  error,
}: ModelFormProps) {
  const { t } = useTranslation();

  // Type-safe handleChange function
  const handleChange = <K extends keyof Model>(field: K, value: Model[K]) => {
    setFormData({ ...formData, [field]: value });
  };

  return (
    <Section>
      <Form
        title={editingModel ? t('model.form_title_edit') : t('model.form_title_create')}
        onSubmit={onSubmit}
        error={error ? t('model.form_error') : undefined}
        actions={
          <>
            <Button type="submit" variant="primary" disabled={isPending}>
              {editingModel
                ? isPending
                  ? t('common.updating')
                  : t('model.form_update_action')
                : isPending
                  ? t('common.creating')
                  : t('model.form_create_action')}
            </Button>
            {editingModel && (
              <Button type="button" variant="secondary" onClick={onCancel}>
                {t('common.cancel')}
              </Button>
            )}
          </>
        }>
        <FormField label={t('model.name')} required>
          <Input
            value={formData.model || ''}
            onChange={e => handleChange('model', e.target.value)}
            placeholder={t('model.name_placeholder')}
          />
        </FormField>

        <FormField label={t('model.context_length')} required>
          <Input
            type="number"
            min="1"
            value={formData.contextLength || ''}
            onChange={e => handleChange('contextLength', parseInt(e.target.value, 10))}
            placeholder={t('model.context_length_placeholder')}
          />
        </FormField>

        <div className="mt-4 grid grid-cols-2 gap-4">
          <FormField label={t('model.can_chat')}>
            <Checkbox
              checked={formData.canChat ?? true}
              onChange={e => handleChange('canChat', e.target.checked)}
            />
          </FormField>
          <FormField label={t('model.can_embed')}>
            <Checkbox
              checked={formData.canEmbed ?? false}
              onChange={e => handleChange('canEmbed', e.target.checked)}
            />
          </FormField>
          <FormField label={t('model.can_prompt')}>
            <Checkbox
              checked={formData.canPrompt ?? true}
              onChange={e => handleChange('canPrompt', e.target.checked)}
            />
          </FormField>
          <FormField label={t('model.can_stream')}>
            <Checkbox
              checked={formData.canStream ?? true}
              onChange={e => handleChange('canStream', e.target.checked)}
            />
          </FormField>
        </div>
      </Form>
    </Section>
  );
}
