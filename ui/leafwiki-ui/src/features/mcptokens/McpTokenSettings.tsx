import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { useMcpTokenStore } from '@/stores/mcpTokens'
import copy from 'copy-to-clipboard'
import { Copy, KeyRound, Loader2, RefreshCw, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { useSetTitle } from '../viewer/setTitle'
import { useToolbarActions } from './useToolbarActions'

function formatDate(value: number | null, fallback: string): string {
  if (value === null) return fallback
  const date = new Date(value * 1000)
  if (Number.isNaN(date.getTime())) return fallback
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(date)
}

export default function McpTokenSettings() {
  const { t } = useTranslation('mcpTokens')
  const {
    metadata,
    isLoading,
    revealedToken,
    error,
    load,
    generate,
    revoke,
    clearRevealed,
  } = useMcpTokenStore()

  const [isGenerating, setIsGenerating] = useState(false)
  const [isRevoking, setIsRevoking] = useState(false)

  useToolbarActions()
  useSetTitle({ title: t('pageTitle') })

  useEffect(() => {
    load()
  }, [load])

  const handleGenerate = async () => {
    setIsGenerating(true)
    try {
      await generate()
      toast.success(t('toast.generated'))
    } catch {
      toast.error(t('toast.generateFailed'))
    } finally {
      setIsGenerating(false)
    }
  }

  const handleRevoke = async () => {
    setIsRevoking(true)
    try {
      await revoke()
      toast.success(t('toast.revoked'))
    } catch {
      toast.error(t('toast.revokeFailed'))
    } finally {
      setIsRevoking(false)
    }
  }

  const handleCopy = () => {
    if (!revealedToken) return
    copy(revealedToken)
    toast.success(t('toast.copied'))
  }

  const hasToken = metadata !== null

  return (
    <div className="settings">
      <h1 className="settings__title">{t('pageTitle')}</h1>
      <p className="settings__section-description">{t('pageDescription')}</p>

      {error && (
        <div className="settings__section">
          <p className="text-error text-sm">{error}</p>
        </div>
      )}

      {revealedToken && (
        <div className="settings__section border-warning/20 bg-warning/5">
          <h2 className="settings__section-title">{t('reveal.title')}</h2>
          <p className="settings__section-description">
            {t('reveal.description')}
          </p>
          <pre
            className="settings__preview font-mono text-sm break-all whitespace-pre-wrap"
            data-testid="mcp-token-reveal-value"
          >
            {revealedToken}
          </pre>
          <p className="text-warning text-sm font-medium">
            {t('reveal.warning')}
          </p>
          <div className="settings__actions">
            <Button
              variant="outline"
              onClick={handleCopy}
              data-testid="mcp-token-copy"
            >
              <Copy className="mr-2 h-4 w-4" />
              {t('reveal.copyButton')}
            </Button>
            <Button onClick={clearRevealed} data-testid="mcp-token-reveal-done">
              {t('reveal.doneButton')}
            </Button>
          </div>
        </div>
      )}

      <div className="settings__section">
        <h2 className="settings__section-title">{t('sectionTitle')}</h2>
        <p className="settings__section-description">
          {t('sectionDescription')}
        </p>

        {isLoading ? (
          <Loader2 className="text-muted h-4 w-4 animate-spin" />
        ) : hasToken ? (
          <>
            <div className="settings__preview">
              <span className="settings__preview-label">
                {t('createdLabel')}
              </span>
              <span className="text-interface-text text-sm">
                {formatDate(metadata.createdAt, '—')}
              </span>
            </div>
            <div className="settings__preview">
              <span className="settings__preview-label">
                {t('expiresLabel')}
              </span>
              <span className="text-interface-text text-sm">
                {formatDate(metadata.expiresAt, '—')}
              </span>
            </div>
            <div className="settings__preview">
              <span className="settings__preview-label">
                {t('lastUsedLabel')}
              </span>
              <span className="text-interface-text text-sm">
                {formatDate(metadata.lastUsedAt, t('neverUsed'))}
              </span>
            </div>
          </>
        ) : (
          <p className="settings__hint">{t('emptyState')}</p>
        )}
      </div>

      {!isLoading && (
        <div className="settings__section">
          <h2 className="settings__section-title">{t('actionsTitle')}</h2>
          <p className="settings__section-description">
            {hasToken ? t('actionsDescription') : t('emptyHint')}
          </p>
          <div className="settings__actions">
            {hasToken ? (
              <>
                <AlertDialog>
                  <AlertDialogTrigger asChild>
                    <Button
                      disabled={isGenerating}
                      data-testid="mcp-token-regenerate"
                    >
                      {isGenerating ? (
                        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                      ) : (
                        <RefreshCw className="mr-2 h-4 w-4" />
                      )}
                      {isGenerating ? t('generating') : t('regenerateButton')}
                    </Button>
                  </AlertDialogTrigger>
                  <AlertDialogContent>
                    <AlertDialogHeader>
                      <AlertDialogTitle>
                        {t('regenerateConfirm.title')}
                      </AlertDialogTitle>
                      <AlertDialogDescription>
                        {t('regenerateConfirm.description')}
                      </AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                      <AlertDialogCancel>
                        {t('regenerateConfirm.cancel')}
                      </AlertDialogCancel>
                      <AlertDialogAction onClick={handleGenerate}>
                        {t('regenerateConfirm.action')}
                      </AlertDialogAction>
                    </AlertDialogFooter>
                  </AlertDialogContent>
                </AlertDialog>

                <AlertDialog>
                  <AlertDialogTrigger asChild>
                    <Button
                      variant="outline"
                      disabled={isRevoking}
                      className="text-error hover:bg-error/10"
                      data-testid="mcp-token-revoke"
                    >
                      {isRevoking ? (
                        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                      ) : (
                        <Trash2 className="mr-2 h-4 w-4" />
                      )}
                      {t('revokeButton')}
                    </Button>
                  </AlertDialogTrigger>
                  <AlertDialogContent>
                    <AlertDialogHeader>
                      <AlertDialogTitle>
                        {t('revokeConfirm.title')}
                      </AlertDialogTitle>
                      <AlertDialogDescription>
                        {t('revokeConfirm.description')}
                      </AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                      <AlertDialogCancel>
                        {t('revokeConfirm.cancel')}
                      </AlertDialogCancel>
                      <AlertDialogAction onClick={handleRevoke}>
                        {t('revokeConfirm.action')}
                      </AlertDialogAction>
                    </AlertDialogFooter>
                  </AlertDialogContent>
                </AlertDialog>
              </>
            ) : (
              <Button
                onClick={handleGenerate}
                disabled={isGenerating}
                data-testid="mcp-token-generate"
              >
                {isGenerating ? (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                ) : (
                  <KeyRound className="mr-2 h-4 w-4" />
                )}
                {isGenerating ? t('generating') : t('generateButton')}
              </Button>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
