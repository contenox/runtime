import { Button, Card, P, Span, Spinner } from '@cate/ui';
import { t } from 'i18next';
import { ChatSession } from '../../../../lib/types';

type ChatListProps = {
  chats: ChatSession[];
  isLoading: boolean;
  error: Error | null;
  onResumeChat: (chatId: string) => void;
};

export function ChatList({ chats, isLoading, error, onResumeChat }: ChatListProps) {
  if (isLoading) {
    return (
      <Card className="flex items-center justify-center p-6">
        <Spinner size="md" />
        <Span className="text-text ml-2">{t('chat.loading_chats')}</Span>
      </Card>
    );
  }

  if (error) {
    // Check if the error object has a message and show it
    const errorMessage = error.message || t('chat.list_error');
    return <Card variant="error">{errorMessage}</Card>;
  }

  return (
    <Card className="divide-surface-200 dark:divide-surface-700 divide-y">
      {chats.length === 0 ? (
        <Span>{t('chat.list_chats_404')}</Span>
      ) : (
        chats.map(chat => (
          <div key={chat.id} className="flex items-center justify-between p-4">
            <div className="space-y-1">
              <div className="flex items-center gap-2">
                <Span className="text-text font-medium">{chat.model}</Span>
                <Span className="text-text-muted text-xs">
                  {new Date(chat.startedAt).toLocaleDateString()}
                </Span>
              </div>
              {chat.lastMessage && (
                <P className="text-text-muted text-sm">
                  {chat.lastMessage.content.slice(0, 50)}...
                </P>
              )}
              <P className="text-text-muted text-xs">
                {t('common.id')}: {chat.id}
              </P>
            </div>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => onResumeChat(chat.id)}
              className="text-primary">
              {t('common.resume')}
            </Button>
          </div>
        ))
      )}
    </Card>
  );
}
