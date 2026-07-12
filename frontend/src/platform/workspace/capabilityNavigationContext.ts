import { createContext } from 'react'
import type { CapabilityId } from '../contracts/modules'

export type CapabilityNavigationIntent = {
  capability: CapabilityId
  input: string
  rightInput?: string
}

export type CapabilityNavigation = {
  hasCapability: (capability: CapabilityId) => boolean
  openCapability: (intent: CapabilityNavigationIntent) => boolean
}

export const CapabilityNavigationContext = createContext<CapabilityNavigation | null>(null)
