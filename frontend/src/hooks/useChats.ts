import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../lib/api';
import { ChatMessage, ChatSession } from '../lib/types';

// Hook for fetching all chat sessions
export function useChats() {
  return useQuery<ChatSession[]>({
    queryKey: ['chats'],
    queryFn: api.getChats,
  });
}

// Hook for creating a chat
export function useCreateChat() {
  const queryClient = useQueryClient();
  return useMutation<Partial<ChatSession>, Error, Partial<ChatSession>>({
    mutationFn: api.createChat,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['chats'] });
    },
  });
}

// Hook for fetching chat history
export function useChatHistory(id: string) {
  return useQuery<ChatMessage[]>({
    queryKey: ['chatHistory', id],
    queryFn: () => api.getChatHistory(id),
    enabled: !!id,
    refetchInterval: 5000,
  });
}

// Hook for sending a message in a chat
export function useSendMessage(chatId: string) {
  const queryClient = useQueryClient();
  return useMutation<ChatMessage[], Error, string>({
    mutationFn: message => api.sendMessage(chatId, message),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['chatHistory', chatId] });
    },
  });
}
export function useChatInstruction(chatId: string) {
  const queryClient = useQueryClient();
  return useMutation<ChatMessage[], Error, string>({
    mutationFn: instruction => api.sendInstruction(chatId, instruction),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['chatHistory', chatId] });
    },
  });
}
