export const API_BASE = import.meta.env.VITE_API_BASE_URL ?? '/api'

export class ApiRequestError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiRequestError'
    this.status = status
  }
}

export function resolveApiUrl(path: string) {
  return new URL(`${API_BASE}${path}`, window.location.origin)
}

export async function apiRequest<T>(input: RequestInfo, init?: RequestInit): Promise<T> {
  const response = await fetch(input, init)
  if (!response.ok) {
    const message = await response.text()
    throw new ApiRequestError(response.status, message || `Request failed: ${response.status}`)
  }
  return (await response.json()) as T
}
