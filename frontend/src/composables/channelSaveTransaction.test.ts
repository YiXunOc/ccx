import { describe, expect, it, vi } from 'vitest'
import { runChannelSaveTransaction } from './channelSaveTransaction'

function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>(res => { resolve = res })
  return { promise, resolve }
}

describe('runChannelSaveTransaction', () => {
  it('waits for staged model persistence before closing and refreshing', async () => {
    const flush = deferred()
    const events: string[] = []
    const close = vi.fn(() => events.push('close'))
    const refresh = vi.fn(async () => { events.push('refresh') })

    const transaction = runChannelSaveTransaction({
      save: async () => { events.push('save'); return { success: true } },
      flush: async () => { events.push('flush:start'); await flush.promise; events.push('flush:end') },
      close,
      refresh,
    })

    await Promise.resolve()
    expect(events).toEqual(['save', 'flush:start'])
    expect(close).not.toHaveBeenCalled()

    flush.resolve()
    await expect(transaction).resolves.toEqual({ success: true })
    expect(events).toEqual(['save', 'flush:start', 'flush:end', 'close', 'refresh'])
  })

  it('keeps the dialog open when staged model persistence fails', async () => {
    const close = vi.fn()
    const refresh = vi.fn()

    await expect(runChannelSaveTransaction({
      save: async () => ({ success: true }),
      flush: async () => { throw new Error('persist failed') },
      close,
      refresh,
    })).rejects.toThrow('persist failed')

    expect(close).not.toHaveBeenCalled()
    expect(refresh).not.toHaveBeenCalled()
  })
})
