import { Button, ButtonGroup, P, Panel, Section } from '@contenox/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { TelegramFrontend } from '../../../../lib/types';

type TelegramCardProps = {
  frontend: TelegramFrontend;
  onEdit: (frontend: TelegramFrontend) => void;
  onDelete: (id: string) => Promise<void>;
};

export default function TelegramCard({ frontend, onEdit, onDelete }: TelegramCardProps) {
  const { t } = useTranslation();
  const [deleting, setDeleting] = useState(false);

  const handleDelete = async () => {
    setDeleting(true);
    try {
      await onDelete(frontend.id);
    } finally {
      setDeleting(false);
    }
  };

  return (
    <Section title={frontend.description || t('telegram.unnamed_frontend')} key={frontend.id}>
      <P>
        {t('telegram.bot_token')}: {frontend.botToken.substring(0, 10)}...
      </P>
      <P>
        {t('telegram.user_id')}: {frontend.userId}
      </P>
      <P>
        {t('telegram.status')}: {frontend.status}
      </P>
      <P>
        {t('telegram.sync_interval')}: {frontend.syncInterval} {t('common.seconds')}
      </P>
      {frontend.chatChain && (
        <P>
          {t('telegram.chat_chain')}: {frontend.chatChain}
        </P>
      )}
      {frontend.lastError && (
        <Panel variant="error" className="mt-2">
          {t('common.last_error')}: {frontend.lastError}
        </Panel>
      )}

      <ButtonGroup className="mt-4">
        <Button variant="ghost" size="sm" onClick={() => onEdit(frontend)}>
          {t('common.edit')}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          onClick={handleDelete}
          disabled={deleting}
          className="text-error">
          {deleting ? t('common.deleting') : t('common.delete')}
        </Button>
      </ButtonGroup>
    </Section>
  );
}
