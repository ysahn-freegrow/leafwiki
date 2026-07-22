import '@/lib/i18n'
import * as mcpApi from '@/lib/api/mcpTokens'
import { useMcpTokenStore } from '@/stores/mcpTokens'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import McpTokenSettings from './McpTokenSettings'

vi.mock('@/lib/api/mcpTokens', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api/mcpTokens')>(
    '@/lib/api/mcpTokens',
  )
  return {
    ...actual,
    getMcpToken: vi.fn(),
    createMcpToken: vi.fn(),
    revokeMcpToken: vi.fn(),
  }
})

const getMcpToken = vi.mocked(mcpApi.getMcpToken)
const createMcpToken = vi.mocked(mcpApi.createMcpToken)

const renderPage = () =>
  render(
    <MemoryRouter>
      <McpTokenSettings />
    </MemoryRouter>,
  )

describe('McpTokenSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useMcpTokenStore.setState({
      metadata: null,
      isLoading: false,
      revealedToken: null,
      error: '',
    })
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders the empty state with a Generate action when there is no token', async () => {
    getMcpToken.mockResolvedValue(null)

    renderPage()

    expect(await screen.findByText('No token yet.')).toBeInTheDocument()
    expect(screen.getByTestId('mcp-token-generate')).toHaveTextContent(
      'Generate token',
    )
    expect(screen.queryByTestId('mcp-token-revoke')).not.toBeInTheDocument()
  })

  it('renders metadata and Regenerate + Revoke actions when a token exists', async () => {
    getMcpToken.mockResolvedValue({
      createdAt: 1_700_000_000,
      expiresAt: 1_700_000_000 + 90 * 24 * 60 * 60,
      lastUsedAt: null,
    })

    renderPage()

    expect(await screen.findByText('Created')).toBeInTheDocument()
    expect(screen.getByText('Expires')).toBeInTheDocument()
    expect(screen.getByText('Last used')).toBeInTheDocument()
    expect(screen.getByText('Never used')).toBeInTheDocument()

    expect(screen.getByTestId('mcp-token-regenerate')).toHaveTextContent(
      'Regenerate token',
    )
    expect(screen.getByTestId('mcp-token-revoke')).toHaveTextContent(
      'Revoke token',
    )
    expect(screen.queryByTestId('mcp-token-generate')).not.toBeInTheDocument()
  })

  it('shows the one-time token value and a copy control after generating', async () => {
    const user = userEvent.setup()
    getMcpToken.mockResolvedValue(null)
    createMcpToken.mockResolvedValue({
      token: 'lw_mcp_secret_token_value_123',
      metadata: {
        createdAt: 1_700_000_000,
        expiresAt: 1_700_000_000 + 90 * 24 * 60 * 60,
        lastUsedAt: null,
      },
    })

    renderPage()

    await user.click(await screen.findByTestId('mcp-token-generate'))

    await waitFor(() => {
      expect(createMcpToken).toHaveBeenCalled()
    })

    expect(
      await screen.findByTestId('mcp-token-reveal-value'),
    ).toHaveTextContent('lw_mcp_secret_token_value_123')
    expect(screen.getByTestId('mcp-token-copy')).toBeInTheDocument()
    expect(
      screen.getByText(
        "This token is shown only once. You won't be able to see it again after closing this view.",
      ),
    ).toBeInTheDocument()
  })
})
