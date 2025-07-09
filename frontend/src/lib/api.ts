import { apiFetch } from './fetch';
import {
  AccessEntry,
  AuthenticatedUser,
  Backend,
  ChainDefinition,
  ChatMessage,
  ChatSession,
  Exec,
  ExecResp,
  FileResponse,
  FolderResponse,
  InProgressJob,
  Job,
  Model,
  ModelListResponse,
  PendingJob,
  Pool,
  SearchResponse,
  StateResponse,
  StatusResponse,
  Trigger,
  UpdateAccessEntryRequest,
  UpdateUserRequest,
  User,
} from './types';

type HttpMethod = 'GET' | 'POST' | 'PUT' | 'DELETE';

interface ApiOptions {
  method?: HttpMethod;
  headers?: Record<string, string>;
  body?: string;
  credentials?: RequestCredentials;
}

const options = (method: HttpMethod, data?: unknown): ApiOptions => {
  const options: ApiOptions = {
    method,
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
  };

  if (data) {
    options.body = JSON.stringify(data);
  }

  return options;
};

interface FormDataApiOptions {
  method?: HttpMethod;
  headers?: Record<string, string>;
  body: FormData;
  credentials?: RequestCredentials;
}

const formDataOptions = (method: HttpMethod, formData: FormData): FormDataApiOptions => {
  // NOTE: We DO NOT set 'Content-Type': 'multipart/form-data'.
  // The browser sets it automatically along with the correct boundary when using FormData.
  return {
    method,
    body: formData,
    credentials: 'same-origin',
    // headers: {} // Intentionally omitted for Content-Type
  };
};

export const api = {
  // Backends
  getBackends: () => apiFetch<Backend[]>('/api/backends'),
  getBackend: (id: string) => apiFetch<Backend>(`/api/backends/${id}`),
  createBackend: (data: Partial<Backend>) =>
    apiFetch<Backend>('/api/backends', options('POST', data)),
  updateBackend: (id: string, data: Partial<Backend>) =>
    apiFetch<Backend>(`/api/backends/${id}`, options('PUT', data)),
  deleteBackend: (id: string) => apiFetch<void>(`/api/backends/${id}`, options('DELETE')),

  // Model State
  createModel: (model: string) => apiFetch<Model>('/api/models', options('POST', { model })),
  getModels: () => apiFetch<ModelListResponse>('/api/models'),
  deleteModel: (model: string) => apiFetch<void>(`/api/models/${model}`, options('DELETE')),

  // Chats
  createChat: ({ model }: Partial<ChatSession>) =>
    apiFetch<Partial<ChatSession>>('/api/chats', options('POST', { model })),

  sendMessage: (id: string, message: string, provider?: string, models?: string[]) =>
    apiFetch<StateResponse>(
      `/api/chats/${id}/chat`,
      options('POST', {
        message,
        provider,
        models: models || [],
      }),
    ),

  sendInstruction: (id: string, instruction: string) =>
    apiFetch<ChatMessage[]>(`/api/chats/${id}/instruction`, options('POST', { instruction })),

  getChatHistory: (id: string) => apiFetch<ChatMessage[]>(`/api/chats/${id}`),
  getChats: () => apiFetch<ChatSession[]>('/api/chats'),

  // Users
  getUsers: (from?: string) =>
    apiFetch<User[]>(from ? `/api/users?from=${encodeURIComponent(from)}` : '/api/users'),
  getUser: (id: string) => apiFetch<User>(`/api/users/${id}`),
  createUser: (data: Partial<AccessEntry>) => apiFetch<User>('/api/users', options('POST', data)),
  updateUser: (id: string, data: UpdateUserRequest) =>
    apiFetch<User>(`/api/users/${id}`, options('PUT', data)),
  deleteUser: (id: string) => apiFetch<void>(`/api/users/${id}`, options('DELETE')),

  getSystemServices: () => apiFetch<string[]>(`/api/system/services`),
  getSystemResources: () => apiFetch<string[]>(`/api/system/resources`),

  getQueue: () => apiFetch<Job[] | null>(`/api/queue`),
  deleteQueueEntry: (model: string) => apiFetch<void>(`/api/queue/${model}`, options('DELETE')),
  queueProgress(): EventSource {
    return new EventSource(`api/queue/inProgress`);
  },

  // Pools
  getPools: () => apiFetch<Pool[]>('/api/pools'),
  getPool: (id: string) => apiFetch<Pool>(`/api/pools/${id}`),
  createPool: (data: Partial<Pool>) => apiFetch<Pool>('/api/pools', options('POST', data)),
  updatePool: (id: string, data: Partial<Pool>) =>
    apiFetch<Pool>(`/api/pools/${id}`, options('PUT', data)),
  deletePool: (id: string) => apiFetch<void>(`/api/pools/${id}`, options('DELETE')),
  getPoolByName: (name: string) => apiFetch<Pool>(`/api/pool-by-name/${name}`),
  listPoolsByPurpose: (purpose: string) => apiFetch<Pool[]>(`/api/pool-by-purpose/${purpose}`),

  // Backend associations
  assignBackendToPool: (poolID: string, backendID: string) =>
    apiFetch<void>(`/api/backend-associations/${poolID}/backends/${backendID}`, options('POST')),
  removeBackendFromPool: (poolID: string, backendID: string) =>
    apiFetch<void>(`/api/backend-associations/${poolID}/backends/${backendID}`, options('DELETE')),
  listBackendsForPool: (poolID: string) =>
    apiFetch<Backend[]>(`/api/backend-associations/${poolID}/backends`),
  listPoolsForBackend: (backendID: string) =>
    apiFetch<Pool[]>(`/api/backend-associations/${backendID}/pools`),

  // Model associations
  assignModelToPool: (poolID: string, modelID: string) =>
    apiFetch<void>(`/api/model-associations/${poolID}/models/${modelID}`, options('POST')),
  removeModelFromPool: (poolID: string, modelID: string) =>
    apiFetch<void>(`/api/model-associations/${poolID}/models/${modelID}`, options('DELETE')),
  listModelsForPool: (poolID: string) =>
    apiFetch<Model[]>(`/api/model-associations/${poolID}/models`),
  listPoolsForModel: (modelID: string) =>
    apiFetch<Pool[]>(`/api/model-associations/${modelID}/pools`),

  // Add to the api object:
  configureProvider: (provider: 'openai' | 'gemini', data: { apiKey: string; upsert: boolean }) =>
    apiFetch<StatusResponse>(`/api/providers/${provider}/configure`, options('POST', data)),

  getProviderStatus: (provider: 'openai' | 'gemini') =>
    apiFetch<StatusResponse>(`/api/providers/${provider}/status`),

  // Access Entries
  getAccessEntries: (expand?: boolean, identity?: string) => {
    const params = new URLSearchParams();
    if (expand) params.append('expand', 'user');
    if (identity) params.append('identity', identity);
    const queryString = params.toString() ? `?${params.toString()}` : '';
    return apiFetch<AccessEntry[]>(`/api/access-control${queryString}`);
  },
  getPermissions: () => apiFetch<string[]>('/api/permissions'),

  getAccessEntry: (id: string) => apiFetch<AccessEntry>(`/api/access-control/${id}`),
  createAccessEntry: (data: Partial<AccessEntry>) =>
    apiFetch<AccessEntry>('/api/access-control', options('POST', data)),
  updateAccessEntry: (id: string, data: UpdateAccessEntryRequest) =>
    apiFetch<AccessEntry>(`/api/access-control/${id}`, options('PUT', data)),
  deleteAccessEntry: (id: string) => apiFetch<void>(`/api/access-control/${id}`, options('DELETE')),

  // Auth endpoints
  login: (data: Partial<User>): Promise<AuthenticatedUser> =>
    apiFetch<AuthenticatedUser>('/api/ui/login', options('POST', data)),
  register: (data: Partial<User>): Promise<AuthenticatedUser> =>
    apiFetch<AuthenticatedUser>('/api/ui/register', options('POST', data)),
  logout: () => apiFetch<void>('/api/ui/logout', options('POST')),
  getCurrentUser: (): Promise<AuthenticatedUser> => apiFetch<AuthenticatedUser>('/api/ui/me'),
  // Queue management
  removeModelFromQueue: (model: string) => apiFetch<void>(`/api/queue/${model}`, options('DELETE')),

  // File management
  getFileMetadata: (id: string) => apiFetch<FileResponse>(`/api/files/${id}`),

  createFile: (formData: FormData) =>
    apiFetch<FileResponse>('/api/files', formDataOptions('POST', formData)),

  updateFile: (id: string, formData: FormData) =>
    apiFetch<FileResponse>(`/api/files/${id}`, formDataOptions('PUT', formData)),

  deleteFile: (id: string) => apiFetch<void>(`/api/files/${id}`, options('DELETE')),

  getDownloadFileUrl: (id: string) => `/api/files/${id}/download`,

  listFiles: (path?: string) => {
    const query = path ? `?path=${encodeURIComponent(path)}` : '';
    return apiFetch<FileResponse[]>(`/api/files${query}`);
  },
  // Folder management
  createFolder: (data: { path: string }) =>
    apiFetch<FolderResponse>('/api/folders', options('POST', data)),

  renameFolder: (id: string, data: { path: string }) =>
    apiFetch<FolderResponse>(`/api/folders/${id}/path`, options('PUT', data)),

  renameFile: (id: string, data: { path: string }) =>
    apiFetch<FileResponse>(`/api/files/${id}/path`, options('PUT', data)),

  // Job management
  listPendingJobs: (cursor?: string) =>
    apiFetch<PendingJob[]>(`/api/jobs/pending${cursor ? `?cursor=${cursor}` : ''}`),

  listInProgressJobs: (cursor?: string) =>
    apiFetch<InProgressJob[]>(`/api/jobs/in-progress${cursor ? `?cursor=${cursor}` : ''}`),

  searchFiles: (
    query: string,
    topk?: number,
    radius?: number,
    epsilon?: number,
    expandFiles?: boolean,
  ) => {
    const params = new URLSearchParams();
    params.append('q', query);
    if (topk !== undefined) {
      params.append('topk', topk.toString());
    }
    if (radius !== undefined) {
      params.append('radius', radius.toString());
    }
    if (epsilon !== undefined) {
      params.append('epsilon', epsilon.toString());
    }
    if (expandFiles !== undefined) {
      params.append('expand', 'files');
    }
    return apiFetch<SearchResponse>(`/api/search?${params.toString()}`);
  },
  execPrompt: (data: Exec) => apiFetch<ExecResp>(`/api/execute`, options('POST', data)),
  getChains: () => apiFetch<ChainDefinition[]>('/api/chains'),
  getChain: (id: string) => apiFetch<ChainDefinition>(`/api/chains/${id}`),
  createChain: (data: ChainDefinition) =>
    apiFetch<ChainDefinition>('/api/chains', options('POST', data)),
  updateChain: (id: string, data: Partial<ChainDefinition>) =>
    apiFetch<ChainDefinition>(`/api/chains/${id}`, options('PUT', data)),
  deleteChain: (id: string) => apiFetch<void>(`/api/chains/${id}`, options('DELETE')),
  getChainTriggers: (chainId: string) => apiFetch<Trigger[]>(`/api/chains/${chainId}/triggers`),
  addChainTrigger: (chainId: string, data: Trigger) =>
    apiFetch<Trigger>(`/api/chains/${chainId}/triggers`, options('POST', data)),
  removeChainTrigger: (chainId: string, triggerId: string) =>
    apiFetch<void>(`/api/chains/${chainId}/triggers/${triggerId}`, options('DELETE')),
};
