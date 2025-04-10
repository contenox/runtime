import { useEffect, useState } from 'react';
import { api } from '../lib/api';
import { DownloadStatus } from '../lib/types';

export function useDownloadProgressSSE() {
  const [statusMap, setStatusMap] = useState<Record<string, DownloadStatus>>({});

  useEffect(() => {
    const source = api.queueProgress();

    source.onmessage = event => {
      try {
        const parsed: DownloadStatus = JSON.parse(event.data);
        setStatusMap(prev => ({
          ...prev,
          [`${parsed.baseUrl}:${parsed.model}`]: parsed,
        }));
      } catch (e) {
        console.error('Failed to parse SSE event:', e);
      }
    };

    source.onerror = err => {
      console.error('SSE error:', err);
      source.close();
    };

    return () => {
      source.close();
    };
  }, []);

  return { statusMap };
}
