import i18n from '../i18n';

const API_BASE_URL = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8081';
const envTimeout = import.meta.env.VITE_API_TIMEOUT;
const parsedTimeout = envTimeout ? parseInt(envTimeout, 10) : NaN;
const API_TIMEOUT = !isNaN(parsedTimeout) ? parsedTimeout : 100000;

export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
    public details?: unknown,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

// --- API Fetch Helper ---
export async function apiFetch<T>(url: string, options?: RequestInit): Promise<T> {
  const controller = new AbortController();
  const timeoutId = setTimeout(() => controller.abort(), API_TIMEOUT);

  try {
    const headers = new Headers(options?.headers);
    const isFormData = options?.body instanceof FormData;

    if (!isFormData && !headers.has('Content-Type')) {
      headers.set('Content-Type', 'application/json');
    }

    headers.set('Accept-Language', i18n.language);

    const response = await fetch(new URL(url, API_BASE_URL).toString(), {
      ...options,
      headers,
      signal: controller.signal,
    });
    clearTimeout(timeoutId);

    if (!response.ok) {
      let errorMessage = i18n.t('errors.unknown');
      let errorDetails = null;
      const contentType = response.headers.get('Content-Type');

      try {
        if (contentType?.includes('application/json')) {
          const errorBody = await response.json();
          errorMessage = errorBody.error || errorBody.message || errorMessage;
          errorDetails = errorBody.details || { serverError: errorBody };
        } else {
          errorMessage = await response.text();
        }
      } catch (error) {
        if (error instanceof Error) {
          errorMessage = response.statusText || errorMessage;
        }
        errorMessage = response.statusText || errorMessage;
      }

      throw new ApiError(errorMessage, response.status, errorDetails);
    }

    try {
      return await response.json();
    } catch (error) {
      throw new ApiError(i18n.t('errors.invalidResponse'), response.status, {
        cause: error,
      });
    }
  } catch (error) {
    clearTimeout(timeoutId);

    if (error instanceof ApiError) {
      throw error;
    }

    if (error instanceof Error && error.name === 'AbortError') {
      throw new ApiError(i18n.t('errors.timeout'), 0);
    }

    if (error instanceof Error) {
      throw new ApiError(error.message, 0);
    }

    throw new ApiError(i18n.t('errors.unknown'), 0);
  }
}
