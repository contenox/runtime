import { Button, GridLayout, P, Panel, Section, Select } from '@contenox/ui';
import { t } from 'i18next';
import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import {
  useChatHistory,
  useChatInstruction,
  useCreateChat,
  useSendMessage,
} from '../../../hooks/useChats';
import { CapturedStateUnit } from '../../../lib/types';
import { ChatInterface } from './components/ChatInterface';
import { MessageInputForm } from './components/MessageInputForm';
import { StateVisualizer } from './components/StateVisualizer';

// Define available providers and models
const PROVIDERS = [
  { value: '', label: t('chat.default_provider') },
  { value: 'openai', label: 'OpenAI' },
  { value: 'ollama', label: 'Ollama' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'vllm', label: 'vLLM' },
];

export default function ChatPage() {
  const { chatId: paramChatId } = useParams<{ chatId: string }>();
  const [message, setMessage] = useState('');
  const [instruction, setInstruction] = useState('');
  const [chatId, setChatId] = useState<string | null>(paramChatId || null);
  const [operationError, setOperationError] = useState<string | null>(null);
  const [selectedProvider, setSelectedProvider] = useState('');
  const [latestState, setLatestState] = useState<CapturedStateUnit[]>([]);

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

    const payload = {
      message: message,
      ...(selectedProvider && { provider: selectedProvider }),
    };

    sendMessage(payload, {
      onSuccess: response => {
        console.log(response);
        setLatestState(response.state);
      },
    });
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
    <GridLayout variant="body" className="h-full">
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
          <>
            <Panel className="flex gap-4 p-4">
              <Select
                options={PROVIDERS}
                value={selectedProvider}
                onChange={e => setSelectedProvider(e.target.value)}
                className="w-48"
              />
            </Panel>
            <Panel className="max-h-55 overflow-auto">
              {operationError && <Panel variant="error"> {operationError}</Panel>}
              {chatHistory && Array.isArray(chatHistory) && (
                <ChatInterface chatHistory={chatHistory} isLoading={isLoading} error={error} />
              )}
            </Panel>
          </>
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
      <Section>
        <StateVisualizer state={latestState} />
      </Section>
    </GridLayout>
  );
}
