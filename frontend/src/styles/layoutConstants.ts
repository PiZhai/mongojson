/**
 * Type-safe accessors for the authoritative layout breakpoint/sizing data
 * defined in `layout-constants.json`. Test code and any future tooling
 * (Storybook, debug overlays, etc.) should import from here rather than
 * hardcoding breakpoint numbers a second time.
 *
 * The JSON file remains the single source of truth; this module only adds
 * TypeScript types and a typed re-export so consumers get compile-time
 * safety and autocomplete.
 */
import layoutConstantsJson from './layout-constants.json'

export interface LayoutSizeTokens {
  sidebarExpandedWidth: number
  sidebarCollapsedWidth: number
  workspaceGap: number
  workspacePrimaryMin: number
  workspacePrimaryMax: number
  workspaceSecondaryMin: number
  workspaceSecondaryMax: number
  workspaceOutlineMin: number
  workspaceOutlineMax: number
  contentPadding: number
}

export type LayoutLayer = 'shell' | 'content'
export type LayoutQueryType = 'media' | 'container'

export interface LayoutBreakpoint {
  value: number
  formula: string
  layer: LayoutLayer
  queryType: LayoutQueryType
  containerName: string | null
}

export type LayoutBreakpointKey =
  | 'shell.stackedBelow'
  | 'content.editorSplitBelow'
  | 'memo.cardDockBelow'
  | 'content.outlineHideBelow'
  | 'content.compactBelow'
  | 'music.workbenchBelow'
  | 'music.compactBelow'
  | 'music.shellWorkbenchBelow'
  | 'music.shellCompactBelow'
  | 'watchParty.stackBelow'
  | 'watchParty.compactBelow'

export interface LayoutConstants {
  description: string
  sizeTokens: LayoutSizeTokens
  breakpoints: Record<LayoutBreakpointKey, LayoutBreakpoint>
}

export const layoutConstants: LayoutConstants = layoutConstantsJson as LayoutConstants

export const sizeTokens: LayoutSizeTokens = layoutConstants.sizeTokens

export const breakpoints: Record<LayoutBreakpointKey, LayoutBreakpoint> = layoutConstants.breakpoints

/** Convenience accessor: returns the numeric pixel value for a breakpoint key. */
export function breakpointValue(key: LayoutBreakpointKey): number {
  return breakpoints[key].value
}
