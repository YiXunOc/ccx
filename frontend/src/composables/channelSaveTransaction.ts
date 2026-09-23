export interface ChannelSaveTransactionOptions<Result> {
  save: () => Promise<Result>
  flush?: () => Promise<void>
  onSaved?: (result: Result) => void
  close: () => void
  refresh: () => Promise<void>
}

export async function runChannelSaveTransaction<Result>({
  save,
  flush,
  onSaved,
  close,
  refresh,
}: ChannelSaveTransactionOptions<Result>): Promise<Result> {
  const result = await save()
  await flush?.()
  onSaved?.(result)
  close()
  await refresh()
  return result
}
