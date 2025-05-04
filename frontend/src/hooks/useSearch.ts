import { useQuery, UseQueryResult } from '@tanstack/react-query';
import { api } from '../lib/api';
import { searchKeys } from '../lib/queryKeys';
import { SearchResponse } from '../lib/types';

export function useSearch(
  query: string,
  topk?: number,
  radius?: number,
  epsilon?: number,
): UseQueryResult<SearchResponse, Error> {
  return useQuery({
    queryKey: searchKeys.query({ query, topk, radius, epsilon }),
    queryFn: () => api.searchFiles(query, topk, radius, epsilon),
  });
}
