import { Button, Card, P } from '@cate/ui';
import { t } from 'i18next';
import { Model } from '../../../../lib/types';

type ModelCardProps = {
  model: Model;
  onDelete: (model: string) => void;
  deletePending: boolean;
};

export function ModelCard({ model, onDelete, deletePending }: ModelCardProps) {
  return (
    <Card key={model.model} className="flex items-center justify-between p-4">
      <div>
        <P variant="cardTitle">{model.model}</P>
        {model.createdAt && (
          <P>
            {t('common.created_at')} {model.createdAt}
          </P>
        )}
        {model.updatedAt && (
          <P>
            {t('common.updated_at')} {model.updatedAt}
          </P>
        )}
      </div>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => onDelete(model.model)}
        className="text-error"
        disabled={deletePending}>
        {deletePending ? t('common.deleting') : t('translation:model.model_delete')}
      </Button>
    </Card>
  );
}
