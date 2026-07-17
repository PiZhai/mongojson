import { useState } from 'react'
import { ConversationWorkspace } from './components/ConversationWorkspace'
import { StewardStatusBar } from './components/StewardStatusBar'
import './styles.css'

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
