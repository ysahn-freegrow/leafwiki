import { create } from 'zustand'
import {
  createMcpToken,
  getMcpToken,
  revokeMcpToken,
  McpTokenMeta,
} from '@/lib/api/mcpTokens'

interface McpTokenState {
  metadata: McpTokenMeta | null
  isLoading: boolean
  revealedToken: string | null
  error: string
  load: () => Promise<void>
  generate: () => Promise<void>
  revoke: () => Promise<void>
  clearRevealed: () => void
}

export const useMcpTokenStore = create<McpTokenState>((set) => ({
  metadata: null,
  isLoading: false,
  revealedToken: null,
  error: '',

  load: async () => {
    set({ isLoading: true })
    try {
      const metadata = await getMcpToken()
      set({ metadata, isLoading: false, error: '' })
    } catch {
      set({ isLoading: false, error: 'Failed to load MCP token' })
    }
  },

  generate: async () => {
    const { token, metadata } = await createMcpToken()
    set({ metadata, revealedToken: token, error: '' })
  },

  revoke: async () => {
    await revokeMcpToken()
    set({ metadata: null, revealedToken: null, error: '' })
  },

  clearRevealed: () => {
    set({ revealedToken: null })
  },
}))
