import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useConsoleChannels } from './useConsoleChannels'

const put = vi.fn().mockResolvedValue(undefined)
const get = vi.fn().mockResolvedValue({ channels: [], metrics: [], stats: {} })
vi.mock('@/composables/useAdminApi', () => ({ useAdminApi: () => ({ put, get }) }))

describe('Desktop logical channel manual model mapping', () => {
  beforeEach(() => { put.mockClear(); get.mockClear() })
  it('synchronizes one mapping to every protocol of a logical channel', async () => {
    const channels = useConsoleChannels()
    channels.channelsByType.value.chat.channels = [{
      index: 0, name: 'group', serviceType: 'openai', baseUrl: 'https://example.test', apiKeys: [],
      logicalChannelUid: 'logical-1', modelMapping: { A: 'B' },
      protocolRoutes: [
        { kind: 'messages', index: 1, name: 'msg', serviceType: 'claude', channelUid: 'msg-1', status: 'active', apiKeys: [], modelMapping: { A: 'B' } },
        { kind: 'chat', index: 3, name: 'chat', serviceType: 'openai', channelUid: 'chat-1', status: 'active', apiKeys: [], modelMapping: { A: 'C' } },
      ],
    } as never]
    await channels.saveChannel({ name: 'group', modelMapping: { A: 'D' } } as never, 0, undefined, 'chat')
    expect(put).toHaveBeenCalledWith('/api/logical-channels/logical-1', expect.objectContaining({ common: expect.objectContaining({ modelMapping: { A: 'D' } }) }))
    expect(put).not.toHaveBeenCalledWith('/api/messages/channels/1', expect.anything())
  })
  it('reconciles a divergent sibling even when the edited route already has the requested mapping', async () => {
    const channels = useConsoleChannels()
    channels.channelsByType.value.chat.channels = [{
      index: 0, name: 'group', serviceType: 'openai', baseUrl: 'https://example.test', apiKeys: [],
      logicalChannelUid: 'logical-1', modelMapping: { A: 'B' },
      protocolRoutes: [
        { kind: 'messages', index: 1, name: 'msg', serviceType: 'claude', channelUid: 'msg-1', status: 'active', apiKeys: [], modelMapping: {} },
        { kind: 'chat', index: 3, name: 'chat', serviceType: 'openai', channelUid: 'chat-1', status: 'active', apiKeys: [], modelMapping: { A: 'B' } },
      ],
    } as never]
    await channels.saveChannel({ name: 'group', modelMapping: { A: 'B' } } as never, 0, undefined, 'chat')
    expect(put).toHaveBeenCalledWith('/api/logical-channels/logical-1', expect.objectContaining({ common: expect.objectContaining({ modelMapping: { A: 'B' } }) }))
  })
})
