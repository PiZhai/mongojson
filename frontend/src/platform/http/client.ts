export const API_BASE = import.meta.env.VITE_API_BASE_URL ?? '/api'

export function resolveApiUrl(path: string) {
  return new URL(`${API_BASE}${path}`, window.location.origin)
}

export async function apiRequest<T>(input: RequestInfo, init?: RequestInit): Promise<T> {
  const response = await fetch(input, init)
  if (!response.ok) {
    const message = await response.text()
    throw new Error(message || `Request failed: ${response.status}`)
  }
  return (await response.json()) as T
}
