import { useState } from 'react'
import { ConversationWorkspace } from './steward/ConversationWorkspace'
import { StewardStatusBar } from './steward/StewardStatusBar'

export function StewardWorkspace() {
  const [statusRefresh, setStatusRefresh] = useState(0)

  return (
    <div className="steward-workspace steward-conversation-workspace">
      <StewardStatusBar refreshToken={statusRefresh} />
      <ConversationWorkspace
        onDataChanged={async () => setStatusRefresh((value) => value + 1)}
      />
    </div>
  )
}
