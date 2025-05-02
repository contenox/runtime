import {
  useMutation,
  UseMutationResult,
  useQuery,
  useQueryClient,
  UseQueryResult,
} from '@tanstack/react-query';
import { api } from '../lib/api';
import { FileResponse } from '../lib/types';

const fileKeys = {
  all: ['files'] as const,
  lists: () => [...fileKeys.all, 'list'] as const,
  details: () => [...fileKeys.all, 'detail'] as const,
  detail: (id: string) => [...fileKeys.details(), id] as const,
  paths: () => [...fileKeys.all, 'paths'] as const,
};

export function useFiles(): UseQueryResult<FileResponse[], Error> {
  return useQuery<FileResponse[], Error>({
    queryKey: fileKeys.lists(),
    queryFn: api.getFiles,
  });
}

export function useFileMetadata(id: string): UseQueryResult<FileResponse, Error> {
  return useQuery<FileResponse, Error>({
    queryKey: fileKeys.detail(id!),
    queryFn: () => api.getFileMetadata(id!),
  });
}

export function useCreateFile(): UseMutationResult<FileResponse, Error, FormData, unknown> {
  const queryClient = useQueryClient();
  return useMutation<FileResponse, Error, FormData>({
    mutationFn: api.createFile,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: fileKeys.lists() });
      queryClient.invalidateQueries({ queryKey: fileKeys.paths() });
    },
  });
}

export function useUpdateFile(): UseMutationResult<
  FileResponse,
  Error,
  { id: string; formData: FormData },
  unknown
> {
  const queryClient = useQueryClient();
  return useMutation<FileResponse, Error, { id: string; formData: FormData }>({
    mutationFn: ({ id, formData }) => api.updateFile(id, formData),
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({ queryKey: fileKeys.lists() });
      queryClient.invalidateQueries({ queryKey: fileKeys.detail(variables.id) });
      queryClient.invalidateQueries({ queryKey: fileKeys.paths() });
    },
  });
}

export function useDeleteFile(): UseMutationResult<void, Error, string, unknown> {
  const queryClient = useQueryClient();
  return useMutation<void, Error, string>({
    mutationFn: api.deleteFile,
    onSuccess: (_, deletedFileId) => {
      queryClient.invalidateQueries({ queryKey: fileKeys.lists() });
      queryClient.removeQueries({ queryKey: fileKeys.detail(deletedFileId) });
      queryClient.invalidateQueries({ queryKey: fileKeys.paths() });
    },
  });
}

export function useListFilePaths(): UseQueryResult<string[], Error> {
  return useQuery<string[], Error>({
    queryKey: fileKeys.paths(),
    queryFn: api.listFilesPaths,
  });
}
