import { Panel, Span, Spinner } from '@contenox/ui';
import { t } from 'i18next';
import { ChatMessage as ApiChatMessage } from '../../../../lib/types';
import { ChatMessage } from './ChatMessage';

export type ChatInterfaceProps = {
  chatHistory?: ApiChatMessage[];
  isLoading: boolean;
  error: Error | null;
};

export const ChatInterface = ({ chatHistory, isLoading, error }: ChatInterfaceProps) => {
  return (
    <div className="flex flex-col gap-4 p-4">
      {isLoading && (
        <Panel variant="surface" className="sticky top-0 animate-pulse">
          <Spinner size="md" />
          <Span variant="muted" className="ml-2">
            {t('chat.loading_history')}
          </Span>
        </Panel>
      )}

      {error && (
        <Panel variant="error" className="sticky top-0">
          {error.message || t('chat.error_history')}
        </Panel>
      )}

      {chatHistory?.map(message => (
        <div key={message.sentAt}>
          <ChatMessage message={message} />
        </div>
      ))}
    </div>
  );
};
