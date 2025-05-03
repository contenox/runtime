import { UseSuspenseQueryResult, useSuspenseQuery } from '@tanstack/react-query';
import { api } from '../lib/api';
import { SearchResponse } from '../lib/types';

export function useSearch(
  query: string,
  topk?: number,
  radius?: number,
  epsilon?: number,
): UseSuspenseQueryResult<SearchResponse, Error> {
  return useSuspenseQuery({
    queryKey: ['search', query, topk, radius, epsilon],
    queryFn: () => api.searchFiles(query, topk, radius, epsilon),
  });
}
