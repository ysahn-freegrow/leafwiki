import { fetchWithAuth } from './auth'

// All timestamps are UNIX SECONDS (multiply by 1000 for a JS Date).
export type McpTokenMeta = {
  createdAt: number
  expiresAt: number
  lastUsedAt: number | null
}

type GetMcpTokenResponse = {
  metadata: McpTokenMeta | null
}

export type CreateMcpTokenResponse = {
  token: string
  metadata: McpTokenMeta
}

export async function getMcpToken(): Promise<McpTokenMeta | null> {
  const res = (await fetchWithAuth(
    '/api/users/me/mcp-token',
  )) as GetMcpTokenResponse
  return res?.metadata ?? null
}

export async function createMcpToken(): Promise<CreateMcpTokenResponse> {
  return (await fetchWithAuth('/api/users/me/mcp-token', {
    method: 'POST',
  })) as CreateMcpTokenResponse
}

export async function revokeMcpToken(): Promise<void> {
  await fetchWithAuth('/api/users/me/mcp-token', {
    method: 'DELETE',
  })
}
