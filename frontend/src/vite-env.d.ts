/// <reference types="vite/client" />

declare module 'virtual:tool-module-catalog' {
  import type { ToolModuleManifest } from './platform/contracts/modules'

  export const moduleCatalog: ToolModuleManifest[]
}
