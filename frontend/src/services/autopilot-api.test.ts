import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ apiKey: 'test-admin-key' })
}))

vi.mock('./api', () => ({
  default: { discoverChannelConfig: vi.fn(), discoverChannelConfigFast: vi.fn() }
}))

function providerTemplatesResponse(providers: unknown[]): Response {
  return {
    ok: true,
    json: vi.fn().mockResolvedValue({ providers })
  } as unknown as Response
}

describe('provider templates cache', () => {
  beforeEach(() => {
    vi.resetModules()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('预取与实际读取应复用同一个请求', async () => {
    const providers = [{ providerId: 'deepseek', displayName: 'DeepSeek' }]
    const fetchMock = vi.fn().mockResolvedValue(providerTemplatesResponse(providers))
    vi.stubGlobal('fetch', fetchMock)

    const { getProviderTemplates, preloadProviderTemplates } = await import('./autopilot-api')
    const preload = preloadProviderTemplates()

    const [, first, second] = await Promise.all([preload, getProviderTemplates(), getProviderTemplates()])

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(first).toEqual(providers)
    expect(second).toEqual(providers)
  })

  it('预取失败后实际读取应重新请求', async () => {
    const providers = [{ providerId: 'mimo', displayName: 'MiMo' }]
    const fetchMock = vi
      .fn()
      .mockRejectedValueOnce(new Error('temporary failure'))
      .mockResolvedValueOnce(providerTemplatesResponse(providers))
    vi.stubGlobal('fetch', fetchMock)

    const { getProviderTemplates, preloadProviderTemplates } = await import('./autopilot-api')

    await preloadProviderTemplates()
    await expect(getProviderTemplates()).resolves.toEqual(providers)
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
})

describe('smart routing diagnose', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('应使用管理密钥提交结构化 dry-run 请求', async () => {
    const payload = {
      mode: 'shadow',
      plan: {
        requestProfile: { Model: 'claude-sonnet-5', TaskClass: 'supervisor' },
        candidates: [],
        fallbackUsed: false,
        mode: 'dry_run'
      }
    }
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: vi.fn().mockResolvedValue(payload)
    } as unknown as Response)
    vi.stubGlobal('fetch', fetchMock)

    const { diagnoseSmartRouting } = await import('./autopilot-api')
    const request = {
      model: 'claude-sonnet-5',
      channelKind: 'messages' as const,
      operation: 'completion',
      estTokens: 20_000,
      toolUseNeed: true
    }
    await expect(diagnoseSmartRouting(request)).resolves.toEqual(payload)
    expect(fetchMock).toHaveBeenCalledTimes(1)

    const [url, options] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/smart-routing/diagnose')
    expect(options.method).toBe('POST')
    expect(options.headers).toMatchObject({
      'Content-Type': 'application/json',
      'x-api-key': 'test-admin-key'
    })
    expect(JSON.parse(options.body as string)).toEqual(request)
  })
})

describe('auto add route discovery', () => {
  beforeEach(() => {
    vi.resetModules()
  })

  it('全量探测所有协议并保留各协议各自成功的模型', async () => {
    const apiModule = await import('./api')
    const discover = vi.mocked(apiModule.default.discoverChannelConfig)
    discover.mockResolvedValue({
      models: { source: 'models_endpoint', items: ['shared', 'messages-only', 'responses-only'], selected: {} },
      protocols: [
        { protocol: 'messages', success: true, successModels: ['shared', 'messages-only'] },
        { protocol: 'responses', success: true, successModels: ['shared', 'responses-only'] },
        { protocol: 'chat', success: true, successModels: ['shared'] },
        { protocol: 'gemini', success: false, failedModels: ['shared'] }
      ],
      capabilities: {} as never,
      recommendation: { channelKind: 'responses', serviceType: 'responses' },
      rateLimit: { initialRpm: 30, effectiveRpm: 15, rateLimited: true, rateLimitedCount: 1 }
    })
    const { discoverAutoAddRoutes } = await import('./autopilot-api')

    await expect(discoverAutoAddRoutes('messages', ['https://example.com'], ['sk-test'])).resolves.toEqual({
      primaryKind: 'responses',
      routes: [
        { channelKind: 'messages', supportedModels: ['shared', 'messages-only'] },
        { channelKind: 'responses', supportedModels: ['shared', 'responses-only'] },
        { channelKind: 'chat', supportedModels: ['shared'] }
      ],
      rateLimitHint: { initialRpm: 30, effectiveRpm: 15, rateLimited: true, rateLimitedCount: 1 }
    })
    const request = discover.mock.calls[0][0]
    expect(request).toEqual({
      baseUrls: ['https://example.com'],
      apiKey: 'sk-test'
    })
    expect(request).not.toHaveProperty('channelKind')
    expect(request).not.toHaveProperty('serviceType')
    expect(request).not.toHaveProperty('probeAllModels')
  })
})

describe('fast discovery', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.clearAllMocks()
  })

  it('仅探一个真实模型并返回单条不含 supportedModels 的路由', async () => {
    const apiModule = await import('./api')
    const discoverFast = vi.mocked(apiModule.default.discoverChannelConfigFast)
    discoverFast.mockResolvedValue({
      primaryKind: 'chat',
      testedModel: 'nim-main',
      streamingSupported: true,
      testedKeyHash: 'abc123',
      rateLimit: { initialRpm: 60, effectiveRpm: 60, rateLimited: false }
    })
    const { discoverFast: discover } = await import('./autopilot-api')

    await expect(discover('messages', ['https://example.com'], ['sk-test', ''])).resolves.toEqual({
      primaryKind: 'chat',
      routes: [{ channelKind: 'chat' }],
      rateLimitHint: { initialRpm: 60, effectiveRpm: 60, rateLimited: false }
    })
    const request = discoverFast.mock.calls[0][0]
    expect(request).toEqual({ baseUrls: ['https://example.com'], apiKeys: ['sk-test'] })
    expect(request).not.toHaveProperty('channelKind')
  })

  it('primaryKind 为空时返回 null 不建渠道', async () => {
    const apiModule = await import('./api')
    vi.mocked(apiModule.default.discoverChannelConfigFast).mockResolvedValue({
      primaryKind: '',
      testedModel: '',
      streamingSupported: false,
      testedKeyHash: '',
      rateLimit: { initialRpm: 60, effectiveRpm: 60, rateLimited: false }
    })
    const { discoverFast: discover } = await import('./autopilot-api')
    await expect(discover('chat', ['https://example.com'], ['sk-test'])).resolves.toBeNull()
  })

  it('images/vectors 不调用 fast discovery 直接添加', async () => {
    const apiModule = await import('./api')
    const discoverFast = vi.mocked(apiModule.default.discoverChannelConfigFast)
    const { discoverFast: discover } = await import('./autopilot-api')
    await expect(discover('images', ['https://example.com'], ['sk-test'])).resolves.toEqual({
      primaryKind: 'images',
      routes: [{ channelKind: 'images' }]
    })
    expect(discoverFast).not.toHaveBeenCalled()
  })
})

describe('custom auto-add without preflight', () => {
  beforeEach(() => { vi.resetModules(); vi.clearAllMocks() })
  afterEach(() => vi.unstubAllGlobals())

  it('默认探测并按探测结果创建', async () => {
    const apiModule = await import('./api')
    vi.mocked(apiModule.default.discoverChannelConfigFast).mockResolvedValue({
      primaryKind: 'chat', testedModel: 'test', streamingSupported: true,
      testedKeyHash: 'hash', rateLimit: { initialRpm: 60, effectiveRpm: 60, rateLimited: false }
    })
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ index: 0 }) })
    vi.stubGlobal('fetch', fetchMock)
    const { submitCustomAutoAdd } = await import('./autopilot-api')
    await submitCustomAutoAdd('messages', { name: 'relay', baseUrls: ['https://relay.example'], apiKeys: ['sk-test'] })
    expect(apiModule.default.discoverChannelConfigFast).toHaveBeenCalledOnce()
    expect(fetchMock.mock.calls[0][0]).toBe('/api/chat/channels/auto-add')
    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toMatchObject({ routes: [{ channelKind: 'chat' }] })
  })

  it('主动跳过时不探测，按所选协议新建且不发送探测速率或路由', async () => {
    const apiModule = await import('./api')
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ index: 0, discoveryStarted: false }) })
    vi.stubGlobal('fetch', fetchMock)
    const { submitCustomAutoAdd } = await import('./autopilot-api')
    const result = await submitCustomAutoAdd('messages', {
      name: 'relay', baseUrls: ['https://relay.example'], apiKeys: ['sk-test'],
      proxyUrl: 'http://proxy.example', placement: 'back'
    }, true)
    expect(apiModule.default.discoverChannelConfigFast).not.toHaveBeenCalled()
    expect(fetchMock.mock.calls[0][0]).toBe('/api/messages/channels/auto-add')
    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({
      name: 'relay', baseUrls: ['https://relay.example'], apiKeys: ['sk-test'],
      proxyUrl: 'http://proxy.example', placement: 'back', createWithoutProbe: true
    })
    expect(result.targetChannelType).toBe('messages')
  })
})
