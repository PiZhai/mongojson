import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { StewardAgentEpisode } from '../types'
import {
  AgentEpisodeProgress,
  AgentEpisodeTurnHistory,
} from './ConversationWorkspace'

function createEpisode(overrides: Partial<StewardAgentEpisode> = {}): StewardAgentEpisode {
  return {
    id: 'episode-1',
    conversation_id: 'conversation-1',
    trigger_message_id: 'message-1',
    trigger_kind: 'conversation',
    goal: '完成测试任务',
    status: 'executing',
    current_round: 2,
    tool_call_count: 3,
    max_rounds: 0,
    max_tool_calls: 0,
    max_duration_seconds: 0,
    no_progress_limit: 3,
    no_progress_count: 0,
    created_at: '2026-07-19T00:00:00Z',
    updated_at: '2026-07-19T00:00:00Z',
    turns: [
      {
        id: 'turn-1',
        episode_id: 'episode-1',
        round_index: 1,
        status: 'completed',
        tool_calls: [{ id: 'call-1', tool_name: 'screen.capture', arguments: {} }],
        tool_results: [{ tool_call_id: 'call-1', tool_name: 'screen.capture', output: { ok: true } }],
      },
      {
        id: 'turn-2',
        episode_id: 'episode-1',
        round_index: 2,
        status: 'executing',
        tool_calls: [
          { id: 'call-2', tool_name: 'fs.get_known_folders', arguments: {} },
          { id: 'call-3', tool_name: 'fs.copy', arguments: {} },
        ],
        tool_results: [{ tool_call_id: 'call-2', tool_name: 'fs.get_known_folders', output: { ok: true } }],
      },
    ],
    ...overrides,
  }
}

describe('agent progress disclosure', () => {
  it('keeps long results compact until the user asks for details', () => {
    const longResult = `结果-${'r'.repeat(400)}`
    const longFailure = `错误-${'f'.repeat(400)}`
    const markup = renderToStaticMarkup(
      <AgentEpisodeProgress episode={createEpisode({ last_result_summary: longResult, failure_summary: longFailure })} />,
    )

    expect(markup).toContain('查看错误详情')
    expect(markup).toContain('aria-expanded="false"')
    expect(markup).toContain('aria-controls=')
    expect(markup).not.toContain(longResult)
    expect(markup).not.toContain(longFailure)
  })

  it('reports the latest turn tools without duplicating tool result names', () => {
    const episode = createEpisode()
    const markup = renderToStaticMarkup(<AgentEpisodeProgress episode={episode} />)

    expect(markup).toContain('当前工具')
    expect(markup).toContain('fs.get_known_folders、fs.copy')
    expect(markup.match(/fs.get_known_folders/g)).toHaveLength(2)
  })

  it('adds an ellipsis only when the configured preview limit is exceeded', () => {
    const shortResult = '任务已经完成'
    const longResult = `开始-${'x'.repeat(400)}`

    expect(renderToStaticMarkup(<AgentEpisodeProgress episode={createEpisode({ last_result_summary: shortResult })} />)).toContain(shortResult)
    const longMarkup = renderToStaticMarkup(<AgentEpisodeProgress episode={createEpisode({ last_result_summary: longResult })} />)
    expect(longMarkup).toContain('…')
    expect(longMarkup).not.toContain(longResult)
  })

  it('keeps paginated tool history collapsed behind an accessible control', () => {
    const markup = renderToStaticMarkup(<AgentEpisodeTurnHistory episode={createEpisode({ turn_count: 82, turns_has_more: true })} initiallyOpen={false} />)

    expect(markup).toContain('查看 82 轮工具记录')
    expect(markup).toContain('aria-expanded="false"')
    expect(markup).toContain('aria-controls=')
    expect(markup).not.toContain('第 1 轮')
  })
})
