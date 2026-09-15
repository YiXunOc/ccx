# Kimi (月之暗面) 配置指南

## 获取 API Key

按使用的产品选择 Key 来源：

- 按量 API：访问 [Kimi 开放平台](https://platform.kimi.com/)，进入「API Key 管理」创建 Key。
- Kimi Code：访问 [Kimi Code 控制台](https://www.kimi.com/code/console)，创建 API Key（最多 5 个，完整 Key 仅显示一次）。

## 官方接口地址

Kimi 同时兼容 OpenAI 和 Anthropic 两种协议。Kimi Code 的两个入口使用同一个 Key，模型列表会按 Key 的会员权限返回：

| 协议 | Base URL | 常用 Endpoint |
|------|----------|----------------|
| OpenAI（按量） | `https://api.moonshot.cn/v1` | `https://api.moonshot.cn/v1/chat/completions` |
| Anthropic（Kimi Code） | `https://api.kimi.com/coding/` | `https://api.kimi.com/coding/v1/messages` |
| OpenAI（Kimi Code） | `https://api.kimi.com/coding/v1` | `https://api.kimi.com/coding/v1/chat/completions` |

在 CCX 的自动添加页面选择 `Kimi`（别名 `Kimi Code`）并粘贴 Key，系统会按候选端点验证并为每个 Key 绑定可用入口；Kimi Code 使用 `GET /coding/v1/models` 发现该 Key 实际可用的模型。

## Kimi Code 模型权限

| 会员档位 | 模型 | 上下文窗口 |
|----------|------|------------|
| Andante | `kimi-for-coding` | 1M |
| Moderato | `k3`、`kimi-for-coding` | K3 256K；`kimi-for-coding` 1M |
| Allegretto 及以上 | `k3`、`kimi-for-coding`、`kimi-for-coding-highspeed` | K3 最高 1M；`kimi-for-coding` 1M；高速版 256K |

`k3` 与 `kimi-for-coding` 均支持 `low`、`high`、`max` 三档思考强度（`k3` 默认 `high`，`kimi-for-coding` 默认 `max`）。关闭 thinking 时，K3 系列与 `kimi-for-coding` 的请求均由 K2.8 Preview（无思考版）处理；`kimi-for-coding-highspeed` 由服务端保持 Thinking。

Claude Code 环境变量中的 `k3[1m]` 只是声明 1M 上下文的客户端写法；API 请求和 CCX 的模型字段仍使用 `k3`。

## 在 CCX 中添加渠道

### 方式一：自动添加 Kimi Code（推荐）

选择 **Kimi** / **Kimi Code**，输入一个或多个 Kimi Code API Key。系统会逐 Key 探测 `https://api.kimi.com/coding/v1/models`，不会用虚构模型发送推理请求；低档套餐不会显示 K3 或高速版。

### 方式二：Chat 入口（OpenAI 兼容协议）

| 字段 | 值 |
|------|-----|
| 名称 | `Kimi`（自定义） |
| 服务类型 | `openai` |
| Base URL | `https://api.moonshot.cn/v1` |
| API Keys | 你的 Moonshot API Key |

#### 配置步骤

1. 进入 CCX 管理界面，选择 **Chat** 入口
2. 点击「添加渠道」
3. 填写以下信息：
   - **名称**：`Kimi`
   - **服务类型**：选择 `OpenAI Chat`
   - **Base URL**：`https://api.moonshot.cn/v1`
   - **API Keys**：粘贴你的 API Key
4. 点击保存

### 方式三：Messages 入口（Anthropic 兼容协议）

适用于 Claude Code CLI 等使用 Claude Messages 协议的工具。

| 字段 | 值 |
|------|-----|
| 名称 | `Kimi Code`（自定义） |
| 服务类型 | `claude` |
| Base URL | `https://api.kimi.com/coding/` |
| API Keys | 你的 Kimi Code API Key |

Kimi Code 端点不需要手工把 `opus`、`sonnet` 或 `haiku` 映射为 Claude 模型名。建议使用 `/coding/v1/models` 返回的真实模型 ID；如客户端发送通用别名，请在 CCX 的模型映射中明确指定目标模型。

### 模型白名单（可选）

```
k3
kimi-for-coding
kimi-for-coding-highspeed
```

## 可用模型

| 模型 | 说明 |
|------|------|
| `k3` | Kimi K3 旗舰编程模型；Moderato 可用 256K，Allegretto 及以上可解锁最高 1M；支持 `low` / `high` / `max` |
| `kimi-for-coding` | Kimi K2.8 Preview（2026-09 起模型 ID 不变、实际模型升级），综合性能接近 K3，1M 上下文，支持 `low` / `high` / `max`（默认 `max`），所有会员可用 |
| `kimi-for-coding-highspeed` | Kimi K2.7 Code 高速版，输出约快 5-6 倍；Allegretto 及以上可用，消耗更高 |
| `kimi-k2.7` | 最新按量计费模型，原生多模态 Agentic 模型 |
| `kimi-k2.6` | 多模态 Agentic 模型，1T 总参 / 32B 激活 |
| `kimi-k2.5` | 多模态 Agentic 模型 |
| `moonshot-v1-auto` | 自动选择上下文长度（旧一代） |
| `moonshot-v1-128k` | 128K 上下文（旧一代） |

::: warning 模型停服通知
`kimi-k2` 将于 **2026/05/25** 停服，请迁移到 `kimi-k2.6`。
:::

## 注意事项

- 按量 OpenAI 兼容 API 的中国区 Base URL 为 `https://api.moonshot.cn/v1`；CCX 仍兼容 `https://api.moonshot.ai/v1` 全球入口
- Kimi Code 的 Anthropic Base URL 为 `https://api.kimi.com/coding/`，OpenAI Base URL 为 `https://api.kimi.com/coding/v1`
- Kimi Code 的 `/coding/v1/models` 会按 Key 返回模型范围；该接口返回 401/403 时先检查 Key 是否有效，而调用未列出的模型或超出上下文权限时，上游也可能返回 401
- `kimi-k2.7` 是当前最新按量计费模型，支持长上下文编码
- `kimi-for-coding` 和 `kimi-for-coding-highspeed` 只应在 Kimi Code 入口使用
