# 协议模型配置前端交付记录

## attempt 2 实现

- LogicalChannel.protocolModelPreferences 为唯一权威；移除先前 Channel 上的类型声明。
- 新增 getLogicalChannel(uid) 读取裸逻辑实体；既有 updateLogicalChannel 类型化，PUT common.protocolModelPreferences。
- EditChannelModal 接入 LogicalProtocolModels，独立保存绑定，不触发物理配置或账号凭证接口。
- 清空最后一个协议选择发送显式空 map；未修改不发送；保存失败保留草稿，不显示成功。
- 缺 logicalChannelUid 或 GET 失败禁编辑；切换渠道隔离异步回包；关闭弹窗销毁并重新打开时 GET 回显。
- ProtocolModelAvailability 使用协议发现并集；多选组件保留未发现绑定并提示，同渠道跨协议冲突提示且禁保存。
- 新增/扩展三组测试共10个目标用例；尚未执行，不计通过。

## 验证

本次 node node_modules/vue-tsc/bin/vue-tsc.js --noEmit 和 git diff --check 均 exit0。
先前标准 bun run type-check / bun run build 均因 Bun 未安装 exit1；等价 Vite 构建及 Vitest 均在 optimizeSafeRealPathSync 的子进程管道处 spawn EPERM，未启动测试或构建。权限明确禁止审批/提权。按队长指示不重复无变化的环境失败。

功能源码接线完成；构建和交互验证仍 BLOCKED_ENVIRONMENT，不能宣布验收通过。未手动编辑产物、依赖或锁文件。
