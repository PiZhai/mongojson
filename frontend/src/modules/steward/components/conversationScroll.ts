export const CONVERSATION_BOTTOM_THRESHOLD = 72

type ScrollMetrics = Pick<HTMLElement, 'scrollHeight' | 'scrollTop' | 'clientHeight'>

export function isConversationNearBottom(
  element: ScrollMetrics,
  threshold = CONVERSATION_BOTTOM_THRESHOLD,
) {
  return element.scrollHeight - element.scrollTop - element.clientHeight <= threshold
}
