# 自动发现调试日志使用指南

## 功能说明

为"重新发现"流程新增调试模式专属日志，记录：
1. 触发时的渠道概况（Key 数量、Base URL 数量）
2. 每个 Key 的候选端点信息（索引、Key ID、绑定状态、候选 URL 数）
3. Checkpoint 复用情况（Key ID、端点 UID、协议状态）
4. 上游请求详情（Key ID、探测头开关）
5. 上游响应结果（HTTP 状态、指纹重试、响应体大小、错误类型）

**关键设计**：
- 仅在开发模式 + 调试级别日志开启时输出
- 使用 SHA256 前 6 字节（12 位十六进制）作为 Key 稳定标识，可跨请求关联但无法反推原 Key
- 不输出 Key 明文或认证头
- 开关在 `NewAutoDiscoveryRunner` 初始化时读取一次并缓存（`debugEnabled` 字段），端点/Key 循环内不重复读环境变量；因此修改 `.env` 后**必须重启服务**才生效

## 启用方法

修改 `backend-go/.env` 并重启服务：

```bash
ENV=development
LOG_LEVEL=debug
```

然后重启：

```bash
make dev
# 或者如果服务已在运行
# 手动停止后重新启动
```

**注意**：`ENV` 和 `LOG_LEVEL` 只能通过环境变量配置，`.config/config.json` 中没有对应字段。

## 日志格式

所有调试日志使用 `[AutoDiscovery-Debug]` 前缀：

```
[AutoDiscovery-Debug] trigger channel=ch_openai_001 keys=3 baseURLs=2
[AutoDiscovery-Debug] endpoint-candidate channel=ch_openai_001 key_index=0 key=a1b2c3d4e5f6 bound=false urls=2
[AutoDiscovery-Debug] upstream-request channel=ch_openai_001 key=a1b2c3d4e5f6 probe_headers=true
[AutoDiscovery-Debug] upstream-result channel=ch_openai_001 key=a1b2c3d4e5f6 status=200 fingerprint_retry_success=false body_bytes=1234
[AutoDiscovery-Debug] checkpoint-reuse channel=ch_openai_001 key=a1b2c3d4e5f6 endpoint=openai_messages_https://api.openai.com protocol_ok=true
```

**字段说明**：
- `key=a1b2c3d4e5f6`：Key 的 SHA256 前 12 位十六进制，同一个 Key 在所有日志中保持相同
- `key_index`：Key 在渠道配置数组中的索引（从 0 开始）
- `bound`：该 Key 是否绑定了专用 Base URL
- `probe_headers`：是否使用探测专用请求头
- `fingerprint_retry_success`：TLS 指纹探测重试是否成功

## 排查流程

### 场景：新增 Key 后旧 Key 恢复可用

1. 开启调试日志后触发重新发现
2. 观察 `trigger` 日志确认 Key 总数变化（keys=2 → keys=3）
3. 检查每个 `endpoint-candidate` 的 `key_index` 和 `key` ID：
   - 记录旧 Key 的 ID（如 `a1b2c3d4e5f6`）
   - 记录新 Key 的 ID（如 `7f8e9d0c1b2a`）
4. 对比新增前后的 `upstream-result`：
   - 旧 Key 的 HTTP 状态是否从 401/403 变为 200
   - `fingerprint_retry_success` 是否从 false 变 true
5. 查看 `checkpoint-reuse`：
   - 是否复用了旧 Key 之前失败的 checkpoint
   - `protocol_ok` 字段是否与实际结果不符

### 场景：发现成功但调用失败

1. 从发现日志中找到成功的 Key ID（如 `a1b2c3d4e5f6`）
2. 在渠道调用日志（`/api/{type}/channels/:id/logs`）中找到失败的调用
3. 比对：
   - 发现时的 HTTP 状态（`upstream-result status=200`）
   - 调用时的 `statusCode`（可能是 500/502）
4. 检查差异：
   - `probe_headers=true` 与实际调用的请求头
   - 发现时的 Base URL 与调用时是否一致

## 关联调用日志

调用日志中的 `keyMask` 字段（如 `sk-***abc`）无法直接与调试日志的 Key ID 对应。需要：

1. 记录问题渠道的所有 Key（或脱敏后缀）
2. 在发现日志中通过 `key_index` 和出现顺序推断
3. 或临时在代码中增加 `log.Printf("[Debug] key_index=%d mask=%s id=%s", keyIndex, utils.MaskAPIKey(key), discoveryKeyID(key))` 建立映射

## 注意事项

- 调试日志**不会**在生产环境（`ENV=production`）或非调试级别（`LOG_LEVEL!=debug`）输出
- Key ID 采用 SHA256 前 48 位，理论碰撞概率约 1/16,777,216，仅用于调试关联
- 旧 Key "可用但发现失败"需结合这些日志与渠道调用日志交叉分析
- 如果怀疑是 checkpoint 缓存问题，观察 `checkpoint-reuse` 的 `protocol_ok` 字段
