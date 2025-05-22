import { Button, Form, FormField, Input } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import { Pool } from '../../../../lib/types';

type PoolFormProps = {
  editingPool: Pool | null;
  onCancel: () => void;
  onSubmit: (e: React.FormEvent) => void;
  isPending: boolean;
  error: boolean;
  name: string;
  setName: (value: string) => void;
  purposeType: string;
  setPurposeType: (value: string) => void;
};

export default function PoolForm({
  editingPool,
  onCancel,
  onSubmit,
  isPending,
  error,
  name,
  setName,
  purposeType,
  setPurposeType,
}: PoolFormProps) {
  const { t } = useTranslation();
  return (
    <Form
      title={editingPool ? t('pools.form_title_edit') : t('pools.form_title_create')}
      onSubmit={onSubmit}
      error={
        error ? `Error ${editingPool ? 'updating' : 'creating'} pool. Please try again.` : undefined
      }
      actions={
        <>
          <Button type="submit" variant="primary" disabled={isPending}>
            {editingPool
              ? isPending
                ? t('common.updating')
                : t('pools.form_update_action')
              : isPending
                ? t('common.creating')
                : t('pools.form_create_action')}
          </Button>
          {editingPool && (
            <Button type="button" variant="secondary" onClick={onCancel}>
              {t('common.cancel')}
            </Button>
          )}
        </>
      }>
      <FormField label={t('common.name')} required>
        <Input value={name} onChange={e => setName(e.target.value)} />
      </FormField>

      <FormField label={t('pools.purpose_type')} required>
        <Input
          value={purposeType}
          onChange={e => setPurposeType(e.target.value)}
          placeholder="General purpose"
        />
      </FormField>
    </Form>
  );
}
