export type Backend = {
  id: string;
  name: string;
  baseUrl: string;
  type: string;
  models: string[];
  pulledModels: OllamaAPIModel[];
  error: string;
  createdAt?: string;
  updatedAt?: string;
};

export type SearchResult = {
  id: string;
  distance: number;
};

export type SearchResponse = {
  results: SearchResult[];
};

export type ModelJob = {
  url: string;
  model: string;
};

export type Job = {
  id: string;
  taskType: string;
  modelJob: ModelJob | undefined;
  scheduledFor: number;
  validUntil: number;
  createdAt: Date;
};

export type OllamaAPIModel = {
  id: string;
  name: string;
  model: string;
};
export type ChatSession = {
  id: string;
  startedAt: string;
  model: string;
  lastMessage?: ChatMessage;
};

export type ChatMessage = {
  role: 'user' | 'assistant' | 'system';
  content: string;
  sentAt: string;
  isUser: boolean;
  isLatest: boolean;
};

export type QueueItem = {
  url: string;
  model: string;
  status: QueueProgressStatus;
};

export type QueueProgressStatus = {
  total: number;
  completed: number;
  status: string;
};

export type Model = {
  id: string;
  model: string;
  createdAt?: string;
  updatedAt?: string;
};

export type Pool = {
  id: string;
  name: string;
  purposeType: string;
  createdAt?: string;
  updatedAt?: string;
};

export type AuthResponse = {
  user: User;
};

export type User = {
  id: string;
  friendlyName: string;
  email: string;
  subject: string;
  password: string;
  createdAt?: string;
  updatedAt?: string;
};

export type DownloadStatus = {
  status: string;
  digest?: string;
  total?: number;
  completed?: number;
  model: string;
  baseUrl: string;
};

export type AccessEntry = {
  id: string;
  identity: string;
  resource: string;
  permission: string;
  createdAt?: string;
  updatedAt?: string;
  identityDetails?: IdentityDetails;
};

export type IdentityDetails = {
  id: string;
  friendlyName: string;
  email: string;
  subject: string;
};

export type UpdateUserRequest = {
  email?: string;
  subject?: string;
  friendlyName?: string;
  password?: string;
};

export type UpdateAccessEntryRequest = {
  identity?: string;
  resource?: string;
  permission?: string;
};

export interface FileResponse {
  id: string;
  path: string;
  content_type: string;
  size: number;
}

export type FolderResponse = {
  id: string;
  path: string;
  createdAt?: string;
  updatedAt?: string;
};

export type PathUpdateRequest = {
  path: string;
};

export interface FileResponse {
  id: string;
  path: string;
  contentType: string;
  size: number;
  createdAt?: string;
  updatedAt?: string;
}

// Create a new type that excludes the password.
export type AuthenticatedUser = Omit<User, 'password'>;

export type PendingJob = {
  id: string;
  taskType: string;
  operation: string;
  subject: string;
  entityId: string;
  scheduledFor: number;
  validUntil: number;
  retryCount: number;
  createdAt: string;
};

export type InProgressJob = PendingJob & {
  leaser: string;
  leaseExpiration: string;
};
