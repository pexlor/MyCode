# 上下文管理 V2 设计

## 1. 背景

MyCode 当前通过 `message.MessageManager.History` 保存会话，并在每次 Agent
迭代时将全部历史消息和全部工具定义发送给模型。随着会话持续，文件内容、工具
结果、重复测试日志和对话过程会持续占用上下文，最终导致成本上升、模型注意力
下降或请求超过模型窗口。

本设计在不引入 Redis、MySQL、向量索引和多 Agent 的前提下，实现一个可落地的
MVP。系统采用四级触发式管理：

0. 按需加载，避免无关内容进入上下文；
1. 工具结果卸载，将新产生的大结果转为可恢复引用；
2. 旧工具结果淘汰，逐步移除已经过时的输出；
3. 会话压缩，将完成的早期轮次转换为结构化任务状态。

## 2. 目标与非目标

### 2.1 目标

- 在每次模型请求前生成满足 Token 预算的上下文视图；
- 原始消息、工具调用和工具结果在本地可审计、可恢复；
- 保证 assistant tool call 与 tool result 的协议配对完整；
- 支持独立配置摘要模型，失败时回退到当前会话模型；
- 对不同模型窗口和输出预算使用统一的预算算法；
- 四层策略彼此独立，可以单测并分阶段启用；
- CLI 退出后仍保留当前会话的 transcript、摘要和工具归档；
- 不持久化或摘要原始隐藏思考，只保留公开结论和执行事实。

### 2.2 非目标

- 不实现跨设备、跨工作区的会话同步；
- 不接入 Redis、MySQL 或对象存储；
- 不实现 embedding、语义检索或自动长期记忆；
- 不实现多 Agent 上下文隔离；
- 不保证任意旧版本会话格式长期兼容；MVP 只定义当前格式版本；
- 不自动清理归档文件，生命周期清理由后续版本处理。

## 3. 核心原则

### 3.1 原始事实与模型视图分离

系统保存两种不同状态：

- `ConversationStore` 保存完整事实，采用追加写入，不因淘汰和压缩而删除；
- `ContextView` 是一次模型请求的临时视图，可以引用、淘汰或压缩内容。

任何上下文策略只能修改 `ContextView`，不能破坏性修改唯一的原始记录。

### 3.2 越早治理，信息损失越小

四级机制不是让每条消息依次经历四次变换。第 0 层在每次请求前装配视图；第 1 层
只在新工具结果产生时判断；第 2 层只在工具历史超出预算时触发；第 3 层只在整体
上下文达到软阈值且存在新的可压缩 Turn 时触发。完成第 3 层后必须重新执行规则
注入和预算校验。

### 3.3 完整轮次是压缩边界

一次 Turn 从用户消息开始，到该请求对应的最终 assistant 文本结束，中间可以包含
多次工具调用：

```text
user
  -> assistant tool calls
  -> tool results
  -> assistant tool calls
  -> tool results
  -> final assistant
```

当前未结束 Turn 不得压缩。工具调用及其结果不得拆开。最近完整轮次策略按
`TurnID` 计算，不能按消息数组下标计算。

## 4. 总体架构

```text
Agent.Run
  -> ConversationStore 追加原始记录
  -> ContextManager.Build
       -> Load Active Summary + Messages After Checkpoint
       -> DemandLoader                 第 0 层，每次装配
       -> ResultOffloader              第 1 层，有新工具结果时触发
       -> StaleResultEvictor           第 2 层，工具历史超预算时触发
       -> ConversationCompactor        第 3 层，整体超预算且有新增量时触发
       -> PersistentRuleInjector       压缩后重新注入
       -> BudgetGuard                  硬预算校验
  -> LLMClient.Stream(ContextView)
```

`ContextManager` 是唯一编排入口：

```go
type ContextManager struct {
	store      ConversationStore
	loader     DemandLoader
	offloader ResultOffloader
	evictor    StaleResultEvictor
	compactor  ConversationCompactor
	estimator  TokenEstimator
	policy     ContextPolicy
}

type BuildInput struct {
	SessionID       string
	CurrentTurnID   string
	SystemPrompt    string
	CurrentRequest  string
	AvailableTools  []*tool.ToolSchema
	Model           ModelContextSpec
}

func (m *ContextManager) Build(
	ctx context.Context,
	in BuildInput,
) (*ContextView, error)
```

Agent 负责追加原始事件和消费 `ContextView`，不持有具体压缩算法。

`Build` 必须先读取最新生效的摘要检查点，只将该检查点之后的原始消息加入候选
视图。其核心语义为：

```go
summary, err := m.store.ActiveSummary(ctx, in.SessionID)
if err != nil {
	return nil, err
}

var messages []StoredMessage
if summary == nil {
	messages, err = m.store.ListMessages(ctx, in.SessionID)
} else {
	messages, err = m.store.ListMessagesAfter(
		ctx,
		in.SessionID,
		summary.CoveredThroughMessageID,
	)
}

view := NewContextView(summary, messages)
```

已经被生效摘要覆盖的消息继续保存在 transcript 中，但不会在下一次 Build 时重新
进入候选视图，也不会被重复压缩。

## 5. 数据模型与存储

### 5.1 会话目录

默认在工作区中保存：

```text
.context/sessions/<session-id>/
├── manifest.json
├── transcript.jsonl
├── summaries/
│   ├── summary-0001.md
│   └── summary-0001.json
└── tool-results/
    ├── call-123.txt
    └── call-123.json
```

`.context/` 默认加入 `.gitignore`。所有路径通过固定会话根目录拼接，禁止模型或工具
输出直接指定归档目标路径。

### 5.2 Manifest

```json
{
  "format_version": 1,
  "session_id": "session-...",
  "workspace": "/absolute/workspace",
  "created_at": "2026-07-22T10:00:00+08:00",
  "updated_at": "2026-07-22T10:30:00+08:00",
  "active_summary_version": 1,
  "covered_through_message_id": "message-120",
  "covered_through_turn_id": "turn-20"
}
```

MVP 读取未知 `format_version` 时必须失败并给出明确错误，不尝试猜测格式。

### 5.3 原始消息

```go
type StoredMessage struct {
	ID          string
	SessionID   string
	TurnID      string
	Iteration   int
	Role        string
	Content     string
	ToolUses    []message.ToolUseBlock
	ToolResults []StoredToolResult
	CreatedAt   time.Time
	TurnStatus  TurnStatus
}

type StoredToolResult struct {
	ToolUseID  string
	ToolName   string
	Content    string
	IsError    bool
	ArtifactID string
	State      ResultState
}

type ResultState string

const (
	ResultFull      ResultState = "full"
	ResultReference ResultState = "reference"
	ResultDropped   ResultState = "dropped"
)
```

`transcript.jsonl` 每行是一条带类型和版本的 JSON 记录，先写临时缓冲再追加并
`fsync`。进程崩溃后只允许忽略最后一条不完整记录，不能跳过中间损坏记录。

### 5.4 工具归档

```go
type ToolArtifact struct {
	ID              string
	SessionID       string
	ToolUseID       string
	ToolName        string
	ArgumentsSummary string
	CreatedAt       time.Time
	IsError         bool
	ByteSize        int64
	TokenEstimate   int
	ContentSHA256   string
	StoragePath     string
	Preview         string
}
```

正文与元数据分别写入 `.txt` 和 `.json`。正文使用临时文件加原子重命名，只有正文
和元数据均成功后才允许将上下文中的完整结果替换为引用。

### 5.5 摘要快照

```go
type SummarySnapshot struct {
	Version                int
	SessionID              string
	CoveredThroughMessageID string
	CoveredThroughTurnID    string
	PreviousSummaryVersion int
	Content                string
	TokenEstimate          int
	ArtifactIDs            []string
	CreatedAt              time.Time
}
```

摘要采用版本链。覆盖游标表示该摘要已经取代的原始消息范围，是下一次 Build 的读取
起点。新摘要成功持久化并通过校验后才更新 manifest 中的 active version 和覆盖游标；
旧摘要不覆盖。未被 manifest 激活的摘要文件不能参与 ContextView 构建。

### 5.6 存储接口

```go
type ConversationStore interface {
	AppendMessage(context.Context, StoredMessage) error
	ListMessages(context.Context, string) ([]StoredMessage, error)
	ListMessagesAfter(context.Context, string, string) ([]StoredMessage, error)
	SaveToolArtifact(context.Context, ToolArtifact, io.Reader) error
	LoadToolArtifact(context.Context, string) (ToolArtifact, io.ReadCloser, error)
	ActiveSummary(context.Context, string) (*SummarySnapshot, error)
	CommitSummary(context.Context, SummarySnapshot, int) error
}
```

MVP 实现 `FileConversationStore`。接口为未来数据库实现保留边界，但本期不实现其他
Store。`CommitSummary` 的最后一个参数是期望的 active version，用于防止并发或
重复提交覆盖较新的检查点。

## 6. ContextView 与预算

```go
type ContextView struct {
	SystemPrompt    string
	Messages        []message.Message
	Tools           []*tool.ToolSchema
	EstimatedTokens int
	Budget          ContextBudget
	AppliedLayers   []LayerReport
}

type ContextBudget struct {
	ContextWindow       int
	ReservedOutput      int
	ReservedToolResults int
	SafetyMargin        int
	HardInputLimit      int
	SoftCompactLimit    int
	ToolHistoryLimit    int
}
```

预算公式：

```text
hard_input_limit = context_window
                 - reserved_output
                 - reserved_tool_results
                 - safety_margin

projected_input = system_prompt
                + selected_tool_schemas
                + persistent_rules
                + current_request
                + rendered_history
```

默认配置：

- `reserved_output`：模型配置的最大输出；未配置时取窗口的 10%，上限 8,192；
- `reserved_tool_results`：窗口的 10%；
- `safety_margin`：窗口的 5%；
- `soft_compact_limit`：`hard_input_limit` 的 75%；
- `tool_history_limit`：`hard_input_limit` 的 25%；
- `single_tool_result_limit`：`hard_input_limit` 的 5%，并限制在 2,000 Token；
- `tool_batch_limit`：`hard_input_limit` 的 15%，并限制在 6,000 Token。

百分比均可配置，但必须验证：

```text
reserved_output + reserved_tool_results + safety_margin < context_window
soft_compact_limit < hard_input_limit
```

### 6.1 Token 估算器

```go
type TokenEstimator interface {
	EstimateText(model string, text string) int
	EstimateMessages(model string, messages []message.Message) int
	EstimateTools(model string, tools []*tool.ToolSchema) int
}
```

优先使用目标模型 tokenizer。没有 tokenizer 时采用保守近似：UTF-8 字节数除以 3，
再增加 15% 安全系数，并计入角色、消息包装和工具 JSON 开销。接口返回估算来源，
便于 `/context` 展示和诊断。

实际 API usage 只能用于校准下一次估算，不能代替当前请求的预估。

## 7. 第 0 层：按需加载

### 7.1 目的

避免无关规则、文件和工具 schema 进入上下文。第 0 层不摘要已有内容，而是控制
候选上下文的入口。

### 7.2 持久规则

MVP 支持：

- System Prompt：始终加载；
- 工作区根 `.agent/context.md`：始终加载；
- 从工作区根到当前活跃文件目录沿途的 `.agent/context.md`：按路径加载；
- 当前用户请求和当前未完成 Turn：始终加载。

根规则和路径规则在第 3 层压缩后从磁盘重新读取，不能依赖摘要保存。为避免内容在
同一次 Build 中变化，Build 开始时读取一次并生成内容哈希。

### 7.3 活跃路径

活跃路径来自确定性证据：

- 当前用户请求中明确引用的工作区路径；
- 当前 Turn 已执行工具的 `file_path`、`path`、`working_directory`；
- 最近一次编辑工具成功修改的文件。

模型推测但未读取的路径不触发规则加载。路径必须解析为工作区内的规范绝对路径。

### 7.4 工具定义选择

MVP 使用确定性分组，不引入额外模型调用：

- 核心组：ReadFile、Grep、Glob，默认加载；
- 编辑组：WriteFile、EditFile，当用户请求包含修改意图或当前 Turn 已编辑时加载；
- 执行组：Bash，当任务涉及构建、测试、命令或当前 Turn 已使用 Bash 时加载；
- MCP 组：仅在用户明确提及对应 MCP 能力，或当前 Turn 已使用该工具时加载。

如果选择器无法可靠判断，回退为加载全部工具，优先保证能力可达，而不是节省 Token。
工具选择只控制发给模型的 schema，不改变权限系统；实际执行仍必须通过
`ToolsManager.Execute`。

### 7.5 输出

第 0 层输出候选消息、选择后的工具 schema、加载规则列表、来源哈希和 Token 占用。
这些数据进入 `LayerReport`，供调试和未来 `/context` 命令使用。

## 8. 第 1 层：工具结果卸载

### 8.1 触发时机

工具执行结束、原始结果追加到 Store 之后、下一次模型请求之前执行。

满足任一条件时卸载：

- 单个结果超过 `single_tool_result_limit`；
- 同一批工具结果合计超过 `tool_batch_limit`；
- 原始请求体可能超过模型或网关字节限制；
- 结果类型明确标记为大型附件。

当前 Agent iteration 不无限豁免。极大结果必须卸载，但可保留更长预览。

### 8.2 归档过程

```text
原始工具结果已写入 transcript
  -> 计算哈希、字节数和 Token
  -> 生成确定性预览与摘要
  -> 原子写入正文和元数据
  -> 校验文件存在且哈希一致
  -> ContextView 中替换为结构化引用
```

归档引用格式：

```text
[工具结果已归档]
工具：Bash
调用：go test ./...
状态：失败
摘要：12 个测试失败，主要位于 internal/context。
关键内容：
- TestArchiveResult: expected 2000, got 0
- TestCompactHistory: orphan tool result
归档 ID：artifact-call-123
路径：.context/sessions/.../tool-results/call-123.txt
SHA256：...
原始大小：156430 tokens / 825631 bytes
需要精确内容时，请按范围读取归档。
```

### 8.3 摘要策略

第 1 层默认不调用 LLM：

- 测试和编译输出：提取退出码、失败项、error、panic 和尾部摘要；
- 搜索输出：保留命中数、涉及文件和首尾命中；
- JSON：保留合法性、顶层字段、元素数量和首尾样本；
- 未识别文本：保留前 N 行、后 N 行、总行数和截断标记。

规则摘要无法生成时仍可使用前后预览。归档引用本身受最大 Token 限制。

### 8.4 失败处理

归档写入或校验失败时不得用引用替换完整结果：

- 如果结果仍低于硬预算，保留完整结果并记录警告；
- 如果结果会突破硬预算，保留安全截断并标记“未归档”，本次模型请求失败；
- 不允许声称一个不存在或哈希不匹配的归档可恢复。

## 9. 第 2 层：旧工具结果淘汰

### 9.1 触发条件

每次 Build 都计算工具历史占用。当其超过 `tool_history_limit` 时，从最低价值的旧
结果开始从 `FULL` 降级为 `REFERENCE`，或从 `REFERENCE` 降级为 `DROPPED`，直到
回到预算内。

当前未完成 Turn 的 tool call/result 必须保留协议配对。其内容可以因单项过大被第
1 层引用，但不能被第 2 层完全移除。

### 9.2 MVP 淘汰规则

使用确定性优先级，不调用 LLM 打分：

1. 已被同一工具、同一目标的新结果覆盖的旧结果最先淘汰；
2. 同一命令重复执行，只完整保留最新一次；
3. 同一文件被重新读取，只完整保留最新版本；
4. 已解决错误的旧日志降级为结论和归档引用；
5. 成功结果优先于未解决的错误证据淘汰；
6. 已归档、恢复成本低的结果优先淘汰；
7. 当前 Turn、最新测试结果和未解决错误最后淘汰。

每次状态变化写入 `LayerReport`，但不回写或删除 transcript 中的原始内容。

### 9.3 协议安全

当 assistant tool call 仍存在于 ContextView 时，对应 tool result 至少保留一个短
引用消息和原 `ToolUseID`。只有第 3 层整体替换一个完整 Turn 后，才可以同时移除
该 Turn 的 tool calls 和 tool results。

## 10. 第 3 层：会话压缩

### 10.1 触发条件

完成前三层后重新估算。如果 `projected_input >= soft_compact_limit`，执行压缩。
硬限制前必须为摘要调用本身留出空间，不能等待 API 返回超限错误。

压缩还必须同时满足：

- 存在 active summary 覆盖游标之后、且已经结束的完整 Turn；
- 除最近保留 Turn 外，新增可压缩内容达到 `min_compaction_increment_tokens`；
- 本次选择的结束消息 ID 晚于当前 `CoveredThroughMessageID`。

默认 `min_compaction_increment_tokens` 为 `hard_input_limit` 的 5%，并限制在至少
4,000 Token。达不到增量条件时不得重新摘要同一段历史；系统应继续使用当前摘要，
必要时由 BudgetGuard 缩短工具引用或最近轮次。

以下内容不参与压缩：

- System Prompt；
- 当前用户请求；
- 当前未完成 Turn；
- 最近 3 个已完成 Turn；
- 当前未解决错误的最小直接证据；
- 持久规则，它们在压缩后重新从磁盘注入。

如果最近 3 个 Turn 本身导致超限，允许逐步减少为 2 个、1 个；当前 Turn 永不压缩。
“最近 3 个 Turn”是默认策略，不是无限豁免。

### 10.2 独立摘要模型

新增非工具化摘要接口：

```go
type Summarizer interface {
	Summarize(context.Context, SummarizeRequest) (SummarizeResponse, error)
}

type SummarizeRequest struct {
	PreviousSummary string
	PreviousCoveredThroughMessageID string
	Messages        []StoredMessage
	ArtifactIndex   []ToolArtifact
	TokenBudget     int
}
```

`LLMClient.Stream` 不直接承担摘要职责。`LLMSummarizer` 可以复用底层 Provider，但
请求必须满足：

- 工具列表为空；
- 独立超时；
- 明确的最大输出 Token；
- temperature 为 0 或提供者允许的最低值；
- 不把摘要请求和响应写入普通会话 Turn；
- 不发送原始隐藏思考内容。

模型选择顺序：

1. 使用 `summary_model` 配置；
2. 初始化失败、超时或请求失败时，回退当前会话模型一次；
3. 回退仍失败时进入确定性最小摘要降级，不重复无限调用。

### 10.3 摘要格式

```markdown
## 当前目标与成功标准
## 用户约束与持久偏好
## 已确认决策
## 已完成工作
## 修改文件与关键变化
## 测试、命令与结论
## 未解决问题
## 可恢复资料
## 待验证事项
## 下一步
```

每条内容标记为“已确认事实”“模型推断”或“待验证事项”。摘要不得提出新方案，
不得把推断提升为事实，归档内容只记录结论、Artifact ID、路径和哈希。

摘要后注入固定边界：

```text
【上下文边界】早期完整轮次已压缩。精确代码、日志、参数和原始响应必须从工作区
文件、transcript 或 Artifact 重新读取，不得根据摘要补全或臆测。
```

### 10.4 摘要提交

```text
读取 active summary 及其覆盖游标
  -> 只选择覆盖游标之后可压缩的完整 Turn
  -> 调用独立摘要模型
  -> 验证结构、大小和引用
  -> 生成更大的 CoveredThroughMessageID / CoveredThroughTurnID
  -> 原子提交新 SummarySnapshot 和 active checkpoint
  -> 生成新的 ContextView
  -> 重新注入持久规则
  -> 重新估算
```

增量摘要输入固定为：

```text
上一版摘要
+ 上一版 CoveredThroughMessageID 之后、截至本次选中结束点的完整 Turn
```

例如 V1 覆盖 Turn 1～15，后续新增 Turn 16～24。生成 V2 时只输入 V1 和 Turn
16～24；V2 覆盖 Turn 1～24。下一次 Build 使用 V2 加 Turn 25 之后的原始消息，
不会重新读取或摘要 Turn 1～24。

`CommitSummary(snapshot, expectedActiveVersion)` 的文件提交顺序：

1. 将摘要正文和元数据写入临时文件；
2. 校验摘要结构、Token、覆盖边界单调递增及引用存在；
3. 原子重命名为正式版本文件；
4. 校验 manifest 的 active version 仍等于 `expectedActiveVersion`；
5. 使用临时文件加原子重命名切换 manifest 的 active version 和覆盖游标。

第 5 步成功前，旧摘要和旧游标继续生效。进程在第 3～4 步崩溃可能留下未激活的
摘要文件，恢复时忽略；绝不能出现游标前进但对应摘要不可读的状态。

如果摘要后仍超过软限制，可以再缩短摘要一次；如果仍超过硬限制，执行降级流程，
不得进入无上限压缩循环。

## 11. BudgetGuard 与防抖

`BudgetGuard` 是发送请求前的最后关卡：

```text
projected_input <= hard_input_limit
```

如果不满足，按以下顺序降级：

1. 将所有非当前 Turn 的工具正文降级为引用；
2. 缩短工具引用预览；
3. 将最近完整 Turn 从 3 个降至 2 个，再降至 1 个；
4. 使用确定性最小任务状态替换 LLM 摘要；
5. 仍超限则返回 `ErrContextBudgetExceeded`，不调用模型。

同一个 Build 最多执行两次 LLM 摘要。若压缩后上下文仍立即超过软限制，记录
`ErrCompactionThrashing` 并停止自动重试，提示用户减少规则、工具或输入内容。

## 12. 配置

新增 `.agent/context.yaml`：

```yaml
version: 1

storage:
  root: .context/sessions

budget:
  soft_compact_ratio: 0.75
  safety_margin_ratio: 0.05
  reserved_tool_result_ratio: 0.10
  tool_history_ratio: 0.25
  default_output_tokens: 8192
  min_compaction_increment_tokens: 4000

retention:
  recent_complete_turns: 3

summary_model:
  enabled: true
  protocol: openai-compat
  provider: ""
  base_url_env: MYCODE_SUMMARY_BASE_URL
  api_key_env: MYCODE_SUMMARY_API_KEY
  model: qwen-plus
  max_output_tokens: 4096
  timeout_seconds: 60
  fallback_to_chat_model: true
```

密钥只能通过环境变量或现有凭据加载机制提供，禁止写入 YAML、日志、摘要、
transcript 和归档元数据。配置缺失时使用安全默认值；配置存在但格式非法时启动失败，
不能静默忽略。

## 13. 与现有代码的集成

建议新增：

```text
internal/context/
├── manager.go
├── policy.go
├── budget.go
├── estimator.go
├── loader.go
├── offloader.go
├── evictor.go
├── compactor.go
├── summarizer.go
├── store.go
├── file_store.go
└── types.go
```

现有代码调整边界：

- `internal/message`：保留 LLM 消息类型，新增或迁移持久化消息类型到
  `internal/context`，避免循环依赖；
- `internal/agent/agent.go`：每次 Stream 前调用 `ContextManager.Build`，工具结果先
  写 Store，再进入下一次 Build；
- `internal/llm`：`LLMSummarizer` 持有独立配置创建的 `LLMClient`，消费现有流式
  接口并显式禁用 Tools；Context 模块只依赖 `Summarizer`，不依赖流式事件细节；
- `internal/tool/tools_manager.go`：增加按名称构建 schema 的方法，执行权限逻辑不变；
- `internal/repl`：创建 SessionID、Store、ContextManager 和可选摘要模型；
- `.gitignore`：加入 `.context/`。

在迁移期，`MessageManager.History` 可以作为当前进程缓存，但 Store 是原始记录的
权威来源。迁移完成后，Agent 不再直接将整个 `History` 发送给模型。

## 14. 可观测性

MVP 先生成结构化 `LayerReport`，CLI 命令可以后续接入：

```go
type LayerReport struct {
	Layer        string
	BeforeTokens int
	AfterTokens  int
	AffectedIDs  []string
	Reason       string
}
```

每次 Build 至少记录：

- Token 估算方法；
- System、规则、工具 schema、历史、当前输入各自占用；
- 四层是否执行及节省的 Token；
- 使用的摘要模型及是否发生回退；
- 最终预算和剩余空间；
- 归档失败、引用失效、摘要失败和防抖事件。

日志不得包含 API Key、完整敏感工具结果和隐藏思考。

## 15. 安全与隐私

- 会话目录权限使用当前用户读写，创建时采用 `0700`；文件默认 `0600`；
- 所有归档路径必须保持在当前 Session 根目录内，拒绝路径穿越和符号链接逃逸；
- 工具参数摘要默认屏蔽名称包含 `token`、`secret`、`password`、`key` 的字段；
- 归档引用使用内部 Artifact ID，路径只作为本地恢复提示；
- 加载归档前验证 SHA256，不匹配时标记失效；
- 不将归档正文自动发送给摘要模型，只发送必要预览和结构化索引；
- 用户明确删除会话时应删除整个精确 Session 目录，但删除命令属于后续功能；
- 现有模型凭据应迁移到环境变量；该安全整改可与 Context V2 初始化一并完成，
  但不属于上下文算法本身。

## 16. 异常处理

| 场景 | 行为 |
| --- | --- |
| transcript 追加失败 | 停止当前 Turn，不执行无法审计的模型请求 |
| 工具归档失败 | 不替换完整结果；可能超硬预算时停止请求 |
| Artifact 文件丢失 | 标记引用失效，不允许模型假定可恢复 |
| Tokenizer 不可用 | 使用保守估算并增加 15% 系数 |
| 独立摘要模型失败 | 回退当前模型一次 |
| 两个摘要模型均失败 | 生成确定性最小任务状态 |
| 摘要结构不合法 | 拒绝提交快照并进入回退 |
| 摘要后仍超限 | 缩短最近轮次和预览，再做硬预算检查 |
| 没有新的可压缩 Turn | 复用 active summary，不调用摘要模型 |
| 连续压缩无收益 | 返回 `ErrCompactionThrashing` |
| 配置非法 | 启动失败并指出字段 |
| Context 取消 | 中止 Build，不提交未完成摘要或归档状态 |

## 17. 测试策略

### 17.1 单元测试

- Budget：不同窗口、输出预留和非法比例；
- Estimator：ASCII、中文、JSON、工具 schema 和 fallback；
- Loader：根规则、路径规则、路径逃逸和工具分组；
- Offloader：单项阈值、批次阈值、原子写入和哈希失败；
- Evictor：重复命令、重复读文件、未解决错误及当前 Turn 保护；
- Compactor：完整 Turn 切分、最近轮次、增量覆盖游标、摘要版本链和模型回退；
- Renderer：tool call/result 始终成对，Artifact 引用保留原 ID；
- Store：JSONL 崩溃尾记录、并发追加、权限和格式版本。

### 17.2 集成测试

- 大工具结果不会直接进入下一次模型请求；
- 连续小测试日志超过工具预算后按优先级淘汰；
- 达到软限制时触发摘要，并重新注入根规则和路径规则；
- 摘要覆盖 Turn 1～15 后，下一次 Build 只装配摘要和 Turn 16 之后的消息；
- 没有超过覆盖游标的新可压缩 Turn 时，不得再次调用摘要模型；
- V2 只消费 V1 与新增完整 Turn，覆盖游标必须单调递增；
- 摘要文件已写入但 manifest 切换失败时，下一次 Build 继续使用 V1；
- 独立摘要模型失败后使用当前模型；
- 两个模型均失败时仍能生成硬预算内的最小 ContextView；
- CLI 重启后可以读取 transcript、摘要和 Artifact；
- 多轮工具调用压缩后不会产生孤立 tool result；
- 最终请求始终小于 `hard_input_limit`。

### 17.3 故障注入

- 磁盘满、无权限、原子重命名失败；
- 摘要超时、返回空内容、返回超预算内容；
- Artifact 被篡改或删除；
- Context 在归档和摘要过程中取消；
- 单个当前用户输入本身超过硬预算。

## 18. 验收标准

1. 每次模型请求都由 `ContextManager.Build` 生成，Agent 不再直接发送完整 History；
2. 单个超限工具结果完整落盘，模型只收到结构化引用；
3. 小工具结果累积超过工具预算时可以渐进淘汰；
4. 达到软限制后只压缩已完成 Turn，当前 Turn 和协议配对完整；
5. 压缩后持久规则从磁盘重新注入；
6. 独立摘要模型可配置，失败后只回退当前模型一次；
7. 摘要、淘汰和归档不删除 transcript 中的原始记录；
8. CLI 退出并重启后，Session 文件可读取并通过哈希校验；
9. 所有模型请求在发送前通过硬预算校验；
10. 摘要或归档失败不会静默丢失信息；
11. tool call 与 tool result 在所有生成视图中保持合法配对；
12. 日志、配置和摘要中不出现模型密钥和原始隐藏思考。
13. Build 只读取 active summary 覆盖游标之后的原始消息，已覆盖消息不会重复进入
    ContextView；
14. 后续摘要只处理上一版摘要和新增完整 Turn，不会重复压缩已覆盖消息；
15. 摘要正文、active version 和覆盖游标的切换满足失败原子性。

## 19. 分阶段实施顺序

### 阶段一：存储、轮次和预算基础

- 引入 Session、TurnID、ConversationStore 和 ContextView；
- 实现 FileConversationStore 和 TokenEstimator fallback；
- Agent 在每次模型请求前经过 ContextManager；
- BudgetGuard 先只报告，不执行压缩。

### 阶段二：工具结果治理

- 实现第 1 层归档和结构化引用；
- 实现第 2 层确定性淘汰；
- 增加归档分段读取能力；
- 启用工具历史预算。

### 阶段三：按需加载

- 实现根规则和路径规则；
- 实现按名称构建工具 schema；
- 加入确定性工具分组选择器；
- 输出 LayerReport。

### 阶段四：会话压缩

- 实现独立 Summarizer 和模型配置；
- 实现摘要版本链、回退和确定性最小摘要；
- 启用软压缩、硬预算与防抖；
- 完成端到端和故障注入测试。

每个阶段结束时都必须保持现有 Agent Loop 可运行，并通过 `go test ./...`。

## 20. 架构决策记录

### ADR-001：采用四级触发式 Context Manager

- **状态：** 接受；
- **决定：** 四级策略通过独立组件由 `ContextManager` 编排，各层按自身条件触发，
  不让每条消息依次经历四次变换；
- **原因：** 相比把逻辑塞入 MessageManager，更容易单测和演进；相比事件溯源，MVP
  复杂度更低；
- **代价：** 类型和接口数量增加，需要明确数据所有权。

### ADR-002：原始记录与 ContextView 分离

- **状态：** 接受；
- **决定：** Store 是事实来源，ContextView 是可丢弃投影；
- **原因：** 摘要有损，调试、恢复和审计需要原文；
- **代价：** 本地磁盘占用增加，需要后续生命周期管理。

### ADR-003：MVP 使用本地会话级持久化

- **状态：** 接受；
- **决定：** 使用 `.context/sessions/<session-id>`，不使用数据库；
- **原因：** 满足跨进程恢复，同时保持部署简单；
- **代价：** 暂不支持跨机器查询和并发多实例共享。

### ADR-004：摘要模型独立配置并回退当前模型

- **状态：** 接受；
- **决定：** 摘要使用 `Summarizer` 抽象，独立模型失败后回退一次；
- **原因：** 允许在成本、速度和质量之间独立选择，同时避免单点失败；
- **代价：** 增加一套模型配置和兼容性测试。

### ADR-005：MVP 使用确定性加载和淘汰规则

- **状态：** 接受；
- **决定：** 第 0、1、2 层不额外调用 LLM；
- **原因：** 行为可预测、成本低且容易测试；
- **代价：** 对复杂语义相关性的判断弱于模型或向量检索，后续可在接口后替换。

### ADR-006：使用持久化摘要检查点避免重复压缩

- **状态：** 接受；
- **决定：** active summary 保存覆盖消息和 Turn 游标，Build 只读取游标之后的原始
  消息；新摘要通过原子 manifest 切换生效；
- **原因：** transcript 必须保留原文，但不能让已压缩消息在每轮重新进入候选视图；
- **代价：** Store 需要检查点一致性、版本冲突和崩溃恢复逻辑。
