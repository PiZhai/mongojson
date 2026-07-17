/** Type-safe access to the project-wide minimum-grid layout contract. */
import layoutConstantsJson from './layout-constants.json'

export interface LayoutSizeTokens {
  sidebarExpandedWidth: number
  sidebarCollapsedWidth: number
  contentPadding: number
  gapXs: number
  gapSm: number
  gapMd: number
  gapLg: number
  editorPaneMin: number
  cardTileMin: number
  summaryTileMin: number
  formFieldMin: number
  inspectPrimaryMin: number
  inspectRailMin: number
  inspectWorkspaceMin: number
  jsonWorkspaceMin: number
  mongoWorkspaceMin: number
  visualizationInputMin: number
  visualizationRailMin: number
  visualizationWorkspaceMin: number
  memoOutlineMin: number
  memoOutlineMax: number
  memoEditorMin: number
  memoPrimaryMin: number
  memoCardRailMin: number
  memoCardRailMax: number
  memoWorkspaceMin: number
  musicSidebarMin: number
  musicSidebarMax: number
  musicMainMin: number
  musicWorkspaceMin: number
  musicMiniInfoMin: number
  musicMiniPlaybackMin: number
  musicMiniActionsMin: number
  musicMiniWorkspaceMin: number
  watchStageMin: number
  watchRailMin: number
  watchWorkspaceMin: number
}

export type LayoutLayer = 'shell' | 'component'
export type LayoutQueryType = 'media' | 'container'

export interface LayoutBreakpoint {
  value: number
  formula: string
  layer: LayoutLayer
  queryType: LayoutQueryType
  containerName: string | null
}

export type LayoutBreakpointKey =
  | 'shell.mobilePortraitMax'
  | 'component.controlsCompactBelow'

export interface LayoutConstants {
  description: string
  sizeTokens: LayoutSizeTokens
  breakpoints: Record<LayoutBreakpointKey, LayoutBreakpoint>
}

export const layoutConstants: LayoutConstants = layoutConstantsJson as LayoutConstants
export const sizeTokens = layoutConstants.sizeTokens
export const breakpoints = layoutConstants.breakpoints

export function breakpointValue(key: LayoutBreakpointKey): number {
  return breakpoints[key].value
}
