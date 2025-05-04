import { Panel, Section, Span, Spinner } from '@cate/ui';
import { t } from 'i18next';
import { cn } from '../../../../lib/utils';
import { ChatMessage } from './ChatMessage';

interface Message {
  role: 'user' | 'assistant' | 'system';
  content: string;
  sentAt: string;
  isUser: boolean;
  isLatest: boolean;
}

export type ChatInterfaceProps = {
  chatHistory?: Message[];
  isLoading: boolean;
  error: Error | null;
  className?: string;
};

export const ChatInterface = ({ chatHistory, isLoading, error, className }: ChatInterfaceProps) => {
  return (
    <Section variant="surface" className={cn('min-h-full', className)}>
      {isLoading && (
        <Panel variant="surface">
          <Spinner size="md" />
          <Span variant="muted" className="ml-2">
            {t('chat.loading_history')}
          </Span>
        </Panel>
      )}

      {error && <Panel variant="error">{error.message || t('chat.error_history')}</Panel>}

      <div className="flex flex-col gap-4 p-4">
        {chatHistory?.map(message => <ChatMessage key={message.sentAt} message={message} />)}
      </div>
    </Section>
  );
};
