import type { Channel, ChannelKind } from '../services/api'
import { isStructurallyEqual } from '../utils/structuralSharing'

export interface ChannelSaveProgress {
  saved?: { result: unknown; payload?: unknown }
  target?: { channel: Channel | null; kind: ChannelKind }
}

export class ChannelExclusionSaveError extends Error {
  readonly cause: unknown

  constructor(cause: unknown) {
    super('渠道已保存，但模型排除部分失败；请重试保存剩余排除项。')
    this.cause = cause
    this.name = 'ChannelExclusionSaveError'
  }
}

export interface ChannelSaveTransactionOptions<Result> {
  progress?: ChannelSaveProgress
  payload?: unknown
  save: (previous?: Result) => Promise<Result>
  flush?: () => Promise<void>
  onSaved?: (result: Result) => void
  close: () => void
  refresh: () => Promise<void>
}

export async function runChannelSaveTransaction<Result>({
  progress = {},
  payload,
  save,
  flush,
  onSaved,
  close,
  refresh,
}: ChannelSaveTransactionOptions<Result>): Promise<Result> {
  // Snapshot before awaiting: later edits must not mutate the saved payload.
  const snapshot = payload === undefined ? undefined : JSON.parse(JSON.stringify(payload))
  const previous = progress.saved
  const result = previous && isStructurallyEqual(previous.payload, snapshot)
    ? previous.result as Result
    : await save(previous?.result as Result | undefined)
  progress.saved = { result, payload: snapshot }
  try {
    await flush?.()
  } catch (error) {
    throw new ChannelExclusionSaveError(error)
  }
  onSaved?.(result)
  close()
  await refresh()
  return result
}
