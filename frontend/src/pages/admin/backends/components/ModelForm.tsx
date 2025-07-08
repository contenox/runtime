import { Button, Form, Input } from '@contenox/ui';
import { useTranslation } from 'react-i18next';

type ModelFormProps = {
  newModel: string;
  onSubmit: (e: React.FormEvent) => void;
  onChange: (e: React.ChangeEvent<HTMLInputElement>) => void;
  isPending: boolean;
};

export default function ModelForm({ newModel, onSubmit, onChange, isPending }: ModelFormProps) {
  const { t } = useTranslation();

  return (
    <Form onSubmit={onSubmit}>
      <Input placeholder={t('model.form_enter_name')} value={newModel} onChange={onChange} />
      <Button type="submit" variant="primary" disabled={isPending}>
        {isPending ? t('common.declaring') : t('model.declare_instruction')}
      </Button>
    </Form>
  );
}
