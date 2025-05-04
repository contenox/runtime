export const poolKeys = {
  all: ['pools'] as const,
  detail: (id: string) => [...poolKeys.all, id] as const,
  backends: (poolID: string) => [...poolKeys.all, poolID, 'backends'] as const,
  models: (poolID: string) => [...poolKeys.all, poolID, 'models'] as const,
  byPurpose: (purpose: string) => [...poolKeys.all, 'purpose', purpose] as const,
  byName: (name: string) => [...poolKeys.all, 'name', name] as const,
};

export const backendKeys = {
  all: ['backends'] as const,
  detail: (id: string) => [...backendKeys.all, id] as const,
  pools: (backendID: string) => [...backendKeys.all, backendID, 'pools'] as const,
};

export const modelKeys = {
  all: ['models'] as const,
  detail: (id: string) => [...modelKeys.all, id] as const,
  pools: (modelID: string) => [...modelKeys.all, modelID, 'pools'] as const,
};

export const stateKeys = {
  all: ['state'] as const,
  pending: ['state', 'pending'] as const,
  inProgress: ['state', 'inprogress'] as const,
};

export const folderKeys = {
  all: ['folders'] as const,
  lists: () => [...folderKeys.all, 'list'] as const,
  details: () => [...folderKeys.all, 'detail'] as const,
  detail: (id: string) => [...folderKeys.details(), id] as const,
};

export const fileKeys = {
  all: ['files'] as const,
  lists: () => [...fileKeys.all, 'list'] as const,
  details: () => [...fileKeys.all, 'detail'] as const,
  detail: (id: string) => [...fileKeys.details(), id] as const,
  paths: () => [...fileKeys.all, 'paths'] as const,
};

export const jobKeys = {
  all: ['jobs'] as const,
  pending: 'pending',
  inprogress: 'inprogress',
};

export const accessKeys = {
  all: ['accessEntries'] as const,
  list: (expand: boolean, identity?: string) => [...accessKeys.all, { expand, identity }] as const,
};

export const permissionKeys = {
  all: ['perms'] as const,
};

export const chatKeys = {
  all: ['chats'] as const,
  history: (chatId: string) => [...chatKeys.all, 'history', chatId] as const,
};

export const userKeys = {
  all: ['users'] as const,
  current: ['user'] as const,
  list: (from?: string) => [...userKeys.all, from] as const,
};

export const systemKeys = {
  all: ['system'] as const,
};

export const searchKeys = {
  all: (query: string, topk?: number, radius?: number, epsilon?: number) =>
    ['search', query, topk, radius, epsilon] as const,
};

export const typeKeys = {
  all: ['types'] as const,
};
