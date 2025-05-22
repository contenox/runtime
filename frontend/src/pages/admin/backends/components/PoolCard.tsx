import { Button, ButtonGroup, P, Section } from '@contenox/ui';
import { useTranslation } from 'react-i18next';
import { Pool } from '../../../../lib/types';

type PoolCardProps = {
  pool: Pool;
  onEdit: (pool: Pool) => void;
  onDelete: (id: string) => Promise<void>;
  isDeleting: boolean;
};

export function PoolCard({ pool, onEdit, onDelete, isDeleting }: PoolCardProps) {
  const { t } = useTranslation();
  return (
    <Section title={pool.name} key={pool.id}>
      <P>
        {t('pools.purpose_type')}: {pool.purposeType}
      </P>

      <ButtonGroup>
        <Button variant="ghost" size="sm" onClick={() => onEdit(pool)}>
          {t('common.edit')}
        </Button>
        <Button variant="ghost" size="sm" onClick={() => onDelete(pool.id)} disabled={isDeleting}>
          {isDeleting ? t('common.deleting') : t('common.delete')}
        </Button>
      </ButtonGroup>
    </Section>
  );
}
