# OpenAI Chat Completions 兼容协议完善设计

## 背景

`internal/llm/openai_compat.go` 当前通过 `openai-go` SDK 调用兼容 OpenAI Chat Completions 的流式接口，并将响应转换为项目内部的 `StreamEvent`。现有实现已支持基础文本、工具调用和 Token 用量，但请求参数映射、工具调用分片、结束状态及流生命周期处理仍不完整。

本次改动继续使用 Chat Completions API，不引入 Responses API，也不改变 `LLMClient` 和 `StreamRequest` 的公开接口。

## 目标

- 完整映射当前 `ModelParm` 中属于标准 Chat Completions 的请求参数。
- 稳定处理文本、Token 用量、单个及多个并行工具调用。
- 支持标准及兼容服务商扩展的非空 `finish_reason`。
- 确保请求取消、空闲超时、流错误和正常结束均能可靠释放资源。
- 将请求构建和流解析拆成可独立测试的单元。
- 为协议关键路径增加自动化测试，不依赖真实模型服务。

## 非目标

- 不支持 Responses API。
- 不新增图片、音频等多模态消息类型。
- 不新增结构化输出、日志概率、指定工具选择等当前内部接口无法表达的能力。
- 不把 `TopK` 作为标准请求字段发送。`top_k` 不是 OpenAI Chat Completions 标准参数，盲目发送会导致部分兼容服务返回请求错误。
- 不处理供应商私有的思考内容字段。
- 不改变多 choice 行为；当前内部流接口只消费默认的第 1 个 choice。

## 方案选择

采用「SDK 负责传输，独立状态机负责解析」的方案：

- `openai-go` SDK 继续负责 HTTP 请求、鉴权、SSE 解码和服务端错误解析。
- 请求构建器负责消息、工具和采样参数转换。
- 流解析器维护一次 completion 的解析状态，将 SDK chunk 转换为内部事件。
- `Stream` 方法只负责编排 SDK 流、空闲超时、上下文取消和事件发送。

相较于直接在现有循环中追加分支，该方案能隔离协议状态并覆盖边界测试；相较于自行实现 SSE，它保留了 SDK 已有的传输能力，避免重复实现底层协议。

## 组件设计

### 请求构建器

新增内部函数，根据 `StreamRequest` 和 `ModelParm` 构造 `openai.ChatCompletionNewParams`。

请求字段规则：

- `model`：始终使用 `ModelParm.ModelName`。
- `messages`：依次加入系统消息和历史消息，保持原顺序。
- `tools`：仅在 `StreamRequest.Tools` 非空时发送，工具参数使用 JSON Schema。
- `stream_options.include_usage`：始终为 `true`。
- `temperature`：`ModelParm.Temp` 非零时发送。现有配置使用数值零值表达「未配置」，因此暂时无法同时区分「未配置」和「显式设置为 0」。
- `top_p`：`ModelParm.TopP` 非零时发送。
- `max_completion_tokens`：`ModelParm.MaxToken` 大于 0 时发送。
- `TopK`、`ContextWindow` 和 `Tinking`：不映射到标准 Chat Completions 请求。

消息转换发生 JSON 序列化错误时，请求构建器返回错误，不再忽略错误。工具结果继续按各自的 `ToolUseID` 生成独立的 tool message。

客户端配置至少要求 `BaseURL`、`APIKey` 和 `ModelName` 非空，错误统一包装 `ErrInvalidConfig`。

### 流解析状态机

解析器按一次 completion 保存以下状态：

- 已观察到的 Token 用量。
- 已观察到的非空 `finish_reason`。
- 按 tool-call `index` 保存的工具 ID、名称、参数文本和开始事件状态。
- 是否已完成，防止重复产生结束事件。

每个 chunk 的处理顺序如下：

1. 如果 chunk 含 usage，则更新完整用量；即使 choices 为空也保留该信息。
2. 仅处理第 1 个 choice，保持现有内部接口语义。
3. 非空文本 delta 产生 `TextStream`。
4. 按 index 合并工具调用的 ID、名称和参数分片。
5. 工具调用首次具备 ID 和名称时产生一次 `ToolCallStart`。
6. 每个非空参数分片产生 `ToolCallStream`。
7. 遇到非空 `finish_reason` 时记录结束原因。
8. 当结束原因为 `tool_calls` 时，按 index 升序完成所有工具调用并产生 `ToolCallComplete`。

工具参数必须是 JSON 对象，以匹配内部的 `map[string]any`。空参数按空对象处理；非法 JSON、非对象 JSON、缺少工具 ID 或名称均返回带上下文的协议解析错误。

如果兼容服务商在 `finish_reason` chunk 之后继续发送 usage-only chunk，解析器先记录结束状态，等 SDK 流真正结束后再产生唯一的 `StreamEnd`，从而保留最终 usage。

### 结束原因

解析器不枚举限制结束原因，而是保存服务端返回的任意非空字符串。因此以下标准值以及兼容服务商扩展值均能透传：

- `stop`
- `tool_calls`
- `length`
- `content_filter`
- 其他非空扩展值

SDK 流正常结束但没有出现非空 `finish_reason` 时返回协议错误，不静默丢失结束事件。

### 流生命周期

`Stream` 方法创建派生 context，并在退出时取消它。SDK 读取 goroutine 向内部 channel 发送 chunk 时同时监听 context，避免外层因超时、取消或解析错误退出后永久阻塞。

空闲计时器在收到任意 chunk 后安全重置。重置前停止计时器，并在需要时排空已触发的信号，避免旧信号造成误超时。

所有向调用方发送事件或错误的操作均监听 context。退出路径遵循以下规则：

- 调用方 context 取消：报告 `ctx.Err()` 并终止 SDK 流。
- 空闲超时：报告 `ErrStreamIdleTimeout` 并终止 SDK 流。
- SDK 流错误：透传 SDK 错误。
- 解析错误：返回包含工具索引或名称的上下文错误。
- 正常结束：完成工具调用后发送一次带结束原因和最终 usage 的 `StreamEnd`。

事件 channel 和错误 channel 均只由外层 goroutine 关闭。

## 错误处理

继续使用现有的 `ErrInvalidConfig`、`ErrInvalidRequest` 和 `ErrStreamIdleTimeout`。协议解析错误使用带上下文的普通错误包装，至少包含失败阶段；工具相关错误包含工具 index，并在已知时包含工具名称。

无效请求在启动异步流之前立即返回关闭的事件 channel 和一个错误值。请求构建错误在流 goroutine 中通过错误 channel 返回，且不发起网络请求。

## 测试设计

### 请求构建测试

- 系统、用户、助手和工具消息的顺序及字段正确。
- 助手消息同时含文本和多个工具调用。
- 多个工具结果分别生成 tool message。
- 工具定义及 JSON Schema 正确。
- `Temperature`、`TopP` 和 `MaxToken` 的映射规则正确。
- 未配置的可选字段不出现在请求 JSON 中。
- 不可序列化的工具参数返回错误。
- 缺少必要客户端配置时返回 `ErrInvalidConfig`。

### 流解析器测试

- 文本分片按顺序产生 `TextStream`。
- usage-only chunk 能更新最终用量。
- 工具 ID、名称和参数位于不同 chunk 时只产生一次开始事件。
- 多个工具调用交错分片时各自正确累计，并按 index 完成。
- 空参数转换为空对象。
- 非法 JSON、非对象 JSON、缺少 ID 或名称返回错误。
- `stop`、`tool_calls`、`length`、`content_filter` 和自定义结束原因均正确透传。
- 结束原因出现后到达的 usage 能进入最终 `StreamEnd`。
- 无结束原因的正常 EOF 返回协议错误。

### 客户端集成测试

使用 `httptest.Server` 模拟 `/chat/completions` SSE 接口，验证：

- 实际请求 JSON 符合设计。
- 文本、工具调用、usage 和结束事件的完整顺序。
- 服务端错误能够返回调用方。
- context 取消能结束流。

空闲超时通过可注入或可测试的内部超时值验证，避免测试等待 5 分钟。

## 验收标准

- `internal/llm/openai_compat.go` 不再包含难以独立验证的大段协议状态逻辑。
- 标准文本流、并行工具调用、usage-only chunk 和所有非空结束原因均有测试覆盖。
- 请求取消、解析失败及超时后没有阻塞的内部发送路径。
- `go test ./internal/llm` 通过。
- `go test ./...` 通过。
- `go vet ./...` 通过。

