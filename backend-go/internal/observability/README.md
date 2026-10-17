# Payload summary（阶段一）

`CCX_PAYLOAD_CAPTURE_MODE` 默认 `off`；`summary`/`all` 均只输出低敏摘要；`failed` 在 attempt 终态仅输出失败记录（取消也属于非成功终态）。使用现有普通 logger 的 `[PayloadSummary]` JSON 与轮转，不新建 capture 目录。

字段边界：

- `clientBodyBytes` 是入口成功解码后、改写前长度，未取得时 `-1`。
- `convertedBodyBytes` 是 provider 构造长度；effort 改写会更新为最终构造长度，不保留转换前/后/最终的三个快照。绕过 provider 的路径可能为 `-1`。
- `contentLength` 是元数据；`getBodyReplayBytes` 只计独立副本，不代表实际发送；`outboundBodyBytes` 没有测量，始终 `-1`。重放错误只输出固定类别。
- `transportTries` 计显式物理 RoundTrip（含 direct-first 直连/代理），`transportTryId` 仅保留最后 ID。没有逐 transport 历史，不包括 Go transport 内部自动重试。
- Responses fold 在 raw frame 计白名单事件，emitter 另计次数；合成终态不进入 raw 计数。standard Responses 在已有接收点统计；Chat 在已有 chunk reader 首读点统计，预检回放不重复。
- 事件类型白名单，未知归 `other`，每类上限 10000。不记录 prompt、tool arguments、输出文本或错误 message。
- `terminal` 是观察到的上游终态；`outcome` 综合 handler 返回、终态与读错误。未接入 EOF 读取点时 `eof=false` 不能证明上游未结束。
- HTTP 错误在原有读体后、错误体写客户端前收口，writer 状态是该收口时刻值，不是整个逻辑请求的最终客户端字节数。
- complete 要求 correlation、attempt、capture、transport、channel 齐全，trace 可为空且允许按 route 改变。

限制：Messages/Gemini 等独立 SSE 路径未接原始事件/读取终态；off 保证零新增 PayloadSummary 日志，不承诺零开销；既有 legacy StreamOutcome 独立存在。emit panic 已隔离，既有其他日志路径不在该保护范围。
