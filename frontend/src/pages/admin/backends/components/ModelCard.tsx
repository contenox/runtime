import { Button, ButtonGroup, P, Section, Span } from '@contenox/ui';
import { t } from 'i18next';
import { Model } from '../../../../lib/types';

type ModelCardProps = {
  model: Model;
  onEdit: (model: Model) => void;
  onDelete: (id: string) => void;
  deletePending: boolean;
};

export function ModelCard({ model, onEdit, onDelete, deletePending }: ModelCardProps) {
  return (
    <Section key={model.id} title={model.model}>
      <div className="flex justify-between">
        <div>
          <P>
            <Span variant="muted">{t('model.context_length')}:</Span> {model.contextLength}
          </P>
          <P>
            <Span variant="muted">{t('model.capabilities')}:</Span>
          </P>
          <ul className="list-inside list-disc pl-2">
            {model.canChat && <li>{t('model.capability_chat')}</li>}
            {model.canEmbed && <li>{t('model.capability_embed')}</li>}
            {model.canPrompt && <li>{t('model.capability_prompt')}</li>}
            {model.canStream && <li>{t('model.capability_stream')}</li>}
          </ul>
          {model.createdAt && (
            <P variant="muted" className="mt-2 text-xs">
              {t('common.created_at')} {new Date(model.createdAt).toLocaleString()}
            </P>
          )}
        </div>
        <ButtonGroup className="flex flex-col items-end gap-2">
          <Button variant="ghost" size="sm" onClick={() => onEdit(model)} className="text-primary">
            {t('common.edit')}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onDelete(model.id)}
            disabled={deletePending}
            className="text-error">
            {deletePending ? t('common.deleting') : t('model.model_delete')}
          </Button>
        </ButtonGroup>
      </div>
    </Section>
  );
}
