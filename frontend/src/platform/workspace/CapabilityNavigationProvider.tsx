import { type PropsWithChildren, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import type { CapabilityId, CapabilityProvider } from '../contracts/modules'
import { CapabilityNavigationContext, type CapabilityNavigation } from './capabilityNavigationContext'
import { saveWorkspaceTransfer } from './transfer'

export function CapabilityNavigationProvider({
  children,
  resolveCapability,
}: PropsWithChildren<{ resolveCapability: (capability: CapabilityId) => CapabilityProvider | undefined }>) {
  const navigate = useNavigate()
  const value = useMemo<CapabilityNavigation>(() => ({
    hasCapability(capability) {
      return Boolean(resolveCapability(capability)?.navigation)
    },
    openCapability(intent) {
      const target = resolveCapability(intent.capability)?.navigation
      if (!target) return false

      saveWorkspaceTransfer({
        target: target.transferTarget,
        mode: target.mode,
        input: intent.input,
        rightInput: intent.rightInput,
      })
      navigate(target.path)
      return true
    },
  }), [navigate, resolveCapability])

  return <CapabilityNavigationContext.Provider value={value}>{children}</CapabilityNavigationContext.Provider>
}
