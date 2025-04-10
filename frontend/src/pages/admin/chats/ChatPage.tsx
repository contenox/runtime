import { Button, P, Panel } from '@cate/ui';
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

  useEffect(() => {
    if (paramChatId) setChatId(paramChatId);
  }, [paramChatId]);

  const { data: chatHistory, isLoading, error } = useChatHistory(chatId || '');
  const { mutate: sendMessage, isPending: isSending } = useSendMessage(chatId || '');
  const { mutate: sendInstruction, isPending: isSendingInstruction } = useChatInstruction(
    chatId || '',
  );
  const { mutate: createChat, isError, error: createError } = useCreateChat();

  const handleSendMessage = (e: React.FormEvent) => {
    e.preventDefault();
    if (!message.trim()) return;
    sendMessage(message);
    setMessage('');
  };

  const handleSendInstruction = (e: React.FormEvent) => {
    e.preventDefault();
    if (!instruction.trim()) return;
    sendInstruction(instruction);
    setInstruction('');
  };

  const handleCreateChat = () => createChat({});

  return (
    <>
      {chatId ? (
        <>
          <MessageInputForm
            value={instruction}
            onChange={setInstruction}
            onSubmit={handleSendInstruction}
            placeholder={t('chat.input_placeholder')}
            isPending={isSendingInstruction}
            buttonLabel={t('chat.send_button')}
          />

          {chatHistory && Array.isArray(chatHistory) && chatHistory.length > 0 && (
            <ChatInterface
              chatHistory={chatHistory}
              isLoading={isLoading}
              error={error}
              className="min-h-full"
            />
          )}
          <MessageInputForm
            value={message}
            onChange={setMessage}
            onSubmit={handleSendMessage}
            isPending={isSending}
          />
        </>
      ) : (
        <div className="flex h-full flex-col items-center justify-center">
          <P className="mb-4">{t('chat.no_chat_selected')}</P>
          <Button onClick={handleCreateChat}>{t('chat.create_chat')}</Button>
        </div>
      )}

      {isError && (
        <Panel variant="error">{createError?.message || t('chat.error_create_chat')}</Panel>
      )}
    </>
  );
}
