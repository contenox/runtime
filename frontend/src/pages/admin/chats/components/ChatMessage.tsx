import { Card, P, Span } from '@cate/ui';
import { t } from 'i18next';
import { cn } from '../../../../lib/utils';

interface Message {
  role: 'user' | 'assistant' | 'system';
  content: string;
  sentAt: string;
  isUser: boolean;
  isLatest: boolean;
}

type ChatMessageProps = {
  message: Message;
};

export const ChatMessage = ({ message }: ChatMessageProps) => {
  return (
    <Card
      className={cn(
        'mb-2 max-w-[85%] rounded-lg p-3',
        message.role === 'user'
          ? 'bg-secondary text-on-secondary ml-auto'
          : 'bg-surface-100 text-text',
      )}>
      <P variant="cardSubtitle">
        {message.role === 'user' ? t('chat.role_user') : t('chat.role_assistant')}
      </P>
      <P>{message.content}</P>
      <Span variant="muted" className="mt-1 text-xs">
        {t('chat.timestamp', {
          datetime: new Date(message.sentAt).toLocaleString(),
        })}
      </Span>
    </Card>
  );
};
