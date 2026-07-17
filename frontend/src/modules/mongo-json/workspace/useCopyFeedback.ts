import { useState } from 'react'
import type { StatusSetter } from './types'

export function useCopyFeedback(setStatus: StatusSetter) {
  const [copied, setCopied] = useState<string | null>(null)

  const copyText = async (value: string, key: string, message: string) => {
    await navigator.clipboard.writeText(value)
    setCopied(key)
    setStatus({ kind: 'success', message })
  }

  return {
    copied,
    copyText,
  }
}
