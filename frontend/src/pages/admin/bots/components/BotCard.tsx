import { Button, ButtonGroup, P, Section } from '@contenox/ui';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Bot } from '../../../../lib/types';

type BotCardProps = {
  bot: Bot;
  onEdit: (bot: Bot) => void;
  onDelete: (id: string) => Promise<void>;
};

export default function BotCard({ bot, onEdit, onDelete }: BotCardProps) {
  const { t } = useTranslation();
  const [deleting, setDeleting] = useState(false);

  const handleDelete = async () => {
    setDeleting(true);
    try {
      await onDelete(bot.id);
    } finally {
      setDeleting(false);
    }
  };

  return (
    <Section title={bot.name} key={bot.id}>
      <P>
        {t('bots.type')}: {bot.botType}
      </P>
      <P>
        {t('bots.job_type')}: {bot.jobType}
      </P>
      <P>
        {t('bots.task_chain_id')}: {bot.taskChainId}
      </P>
      <P>
        {t('bots.last_updated')}: {new Date(bot.updatedAt).toLocaleString()}
      </P>

      <ButtonGroup className="mt-4">
        <Button variant="ghost" size="sm" onClick={() => onEdit(bot)}>
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
