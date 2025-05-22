import { Button, P, Panel, Section } from '@contenox/ui';
import { t } from 'i18next';
import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import {
  useChatHistory,
  useChatInstruction,
  useCreateChat,
  useSendMessage,
} from '../../../hooks/useChats';
import { ChatInterface } from './components/ChatInterface';
import { MessageInputForm } from './components/MessageInputForm';

export default function ChatPage() {
  const { chatId: paramChatId } = useParams<{ chatId: string }>();
  const [message, setMessage] = useState('');
  const [instruction, setInstruction] = useState('');
  const [chatId, setChatId] = useState<string | null>(paramChatId || null);
  const [operationError, setOperationError] = useState<string | null>(null);

  useEffect(() => {
    if (paramChatId) setChatId(paramChatId);
  }, [paramChatId]);

  const { data: chatHistory, isLoading, error } = useChatHistory(chatId || '');
  const {
    mutate: sendMessage,
    isPending: isSending,
    error: sendError,
  } = useSendMessage(chatId || '');

  const {
    mutate: sendInstruction,
    isPending: isSendingInstruction,
    error: instructionError,
  } = useChatInstruction(chatId || '');

  const { mutate: createChat, isError, error: createError } = useCreateChat();

  useEffect(() => {
    const errorMessage = sendError?.message || instructionError?.message;
    if (errorMessage) {
      setOperationError(errorMessage);
      const timer = setTimeout(() => setOperationError(null), 5000);
      return () => clearTimeout(timer);
    }
  }, [sendError, instructionError]);

  const handleSendMessage = (e: React.FormEvent) => {
    e.preventDefault();
    setOperationError(null);
    if (!message.trim()) return;
    sendMessage(message);
    setMessage('');
  };

  const handleSendInstruction = (e: React.FormEvent) => {
    e.preventDefault();
    setOperationError(null);
    if (!instruction.trim()) return;
    sendInstruction(instruction);
    setInstruction('');
  };

  const handleCreateChat = () => createChat({});

  return (
    <Section>
      <MessageInputForm
        title={t('chat.system_input')}
        value={instruction}
        onChange={setInstruction}
        onSubmit={handleSendInstruction}
        placeholder={t('chat.system_input_placeholder')}
        isPending={isSendingInstruction}
        buttonLabel={t('chat.send_button')}
      />
      {chatId ? (
        <Panel className="max-h-55 overflow-auto">
          {operationError && <Panel variant="error"> {operationError}</Panel>}
          {chatHistory && Array.isArray(chatHistory) && (
            <ChatInterface chatHistory={chatHistory} isLoading={isLoading} error={error} />
          )}
        </Panel>
      ) : (
        <div className="flex flex-grow flex-col items-center justify-center">
          {' '}
          <P className="mb-4">{t('chat.no_chat_selected')}</P>
          <Button onClick={handleCreateChat}>{t('chat.create_chat')}</Button>
        </div>
      )}
      <MessageInputForm
        value={message}
        onChange={setMessage}
        onSubmit={handleSendMessage}
        isPending={isSending}
        title={t('chat.chat_input')}
      />
      {isError && (
        <Panel variant="error"> {createError?.message || t('chat.error_create_chat')}</Panel>
      )}
    </Section>
  );
}
