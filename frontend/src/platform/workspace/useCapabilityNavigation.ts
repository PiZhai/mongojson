import { useContext } from 'react'
import { CapabilityNavigationContext } from './capabilityNavigationContext'

export function useCapabilityNavigation() {
  const context = useContext(CapabilityNavigationContext)
  if (!context) throw new Error('useCapabilityNavigation must be used inside CapabilityNavigationProvider.')
  return context
}
