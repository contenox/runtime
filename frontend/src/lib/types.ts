export type Backend = {
  id: string;
  name: string;
  baseUrl: string;
  type: string;
  models: string[];
  pulledModels: Model[];
  error: string;
  createdAt?: string;
  updatedAt?: string;
};

export type SearchResult = {
  id: string;
  resourceType: string;
  distance: number;
  fileMeta: FileResponse;
};

export type StatusResponse = {
  configured: boolean;
  provider: string;
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

export type ModelListResponse = {
  data: OpenAIModel[];
  object: string;
};

export type OpenAIModel = {
  id: string;
  object: string;
  created: string;
  owned_by: string;
};

export type ChatSession = {
  id: string;
  startedAt: string;
  model: string;
  lastMessage?: ChatMessage;
};

export type CapturedStateUnit = {
  taskID: string;
  taskType: string;
  inputType: string;
  outputType: string;
  transition: string;
  duration: string;
  error: ErrorState;
};

export type ErrorState = {
  error: string | null;
};

export type StateResponse = {
  response: string;
  state: CapturedStateUnit[];
};

export type ChatMessage = {
  role: 'user' | 'assistant' | 'system';
  content: string;
  sentAt: string;
  isUser: boolean;
  isLatest: boolean;
  state?: CapturedStateUnit[];
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
  resourceType: string;
  permission: string;
  createdAt?: string;
  updatedAt?: string;
  identityDetails?: IdentityDetails;
  fileDetails?: filesDetails;
};

export type filesDetails = {
  id: string;
  path: string;
  type: string;
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

export type Exec = {
  prompt: string;
};

export type ExecResp = {
  id: string;
  response: string;
};

export type ChainTaskType =
  | 'condition_key'
  | 'parse_number'
  | 'parse_score'
  | 'parse_range'
  | 'raw_string'
  | 'hook';

export type OperatorTerm =
  | 'equals'
  | 'contains'
  | 'starts_with'
  | 'ends_with'
  | '>'
  | 'gt'
  | '<'
  | 'lt'
  | 'in_range'
  | 'default';

export type TriggerType = 'manual' | 'keyword' | 'embedding' | 'webhook';

export interface HookCall {
  type: string;
  args: Record<string, string>;
}

export interface TransitionBranch {
  operator?: OperatorTerm;
  when: string;
  goto: string;
}

export interface TaskTransition {
  on_failure: string;
  branches: TransitionBranch[];
}

export interface ChainTask {
  id: string;
  description: string;
  type: ChainTaskType;
  valid_conditions?: Record<string, boolean>;
  hook?: HookCall;
  print?: string;
  prompt_template: string;
  transition: TaskTransition;
  timeout?: string;
  retry_on_failure?: number;
}

export interface Trigger {
  type: TriggerType;
  description: string;
  pattern?: string;
}

export interface ChainDefinition {
  id: string;
  description: string;
  tasks: ChainTask[];
  token_limit?: number;
  routing_strategy?: string;
}

export interface ChainWithTrigger {
  triggers?: Trigger[];
  chain_definition: ChainDefinition;
}

export type ActivityLog = {
  id: string;
  operation: string;
  subject: string;
  start: string;
  end?: string;
  error?: string;
  entityID?: string;
  entityData?: undefined;
  durationMS?: number;
  metadata?: Record<string, string>;
};

export type ActivityLogsResponse = ActivityLog[];
