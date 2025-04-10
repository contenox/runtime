import { Section } from '@cate/ui';
import { t } from 'i18next';
import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useChats, useCreateChat } from '../../../../hooks/useChats';
import { useModels } from '../../../../hooks/useModels';
import { ChatSession } from '../../../../lib/types';
import { ChatList } from './ChatList';
import { ChatsForm } from './ChatsForm';

export default function ChatsListPage() {
  const navigate = useNavigate();
  const [selectedModel, setSelectedModel] = useState('');

  const { data: modelsData, error: modelsError, isLoading: modelsLoading } = useModels();
  const createChatMutation = useCreateChat();
  const { data: chats, isLoading, error } = useChats();

  useEffect(() => {
    if (modelsData && modelsData.length > 0 && !selectedModel) {
      setSelectedModel(modelsData[0].model);
    }
  }, [modelsData, selectedModel]);

  const handleStartChat = (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedModel) return;
    createChatMutation.mutate(
      { model: selectedModel },
      {
        onSuccess: (data: Partial<ChatSession>) => {
          if (data?.id) navigate(`/chat/${data.id}`);
        },
      },
    );
  };

  const resetForm = () => {
    setSelectedModel(modelsData?.[0]?.model || '');
  };

  const handleResumeChat = (chatId: string) => {
    navigate(`/chat/${chatId}`);
  };

  // Construct error message
  const errorMessage = modelsError
    ? t('chat.models_error', 'Failed to load models: {{error}}', { error: modelsError.message })
    : createChatMutation.error
      ? t('chat.create_error', 'Failed to create chat: {{error}}', {
          error: createChatMutation.error.message,
        })
      : undefined;

  return (
    <>
      <ChatsForm
        onSubmit={handleStartChat}
        isPending={modelsLoading || createChatMutation.isPending}
        onCancel={resetForm}
        models={modelsData ? modelsData.map(m => m.model) : []}
        error={errorMessage}
        selectedModel={selectedModel}
        setSelectedModel={setSelectedModel}
      />
      <Section title={t('chat.personal_chat_list_title')}>
        <ChatList
          chats={chats || []}
          isLoading={isLoading}
          error={error}
          onResumeChat={handleResumeChat}
        />
      </Section>
    </>
  );
}
