# 内置会话命令设计

## 1. 背景

MyCode 的交互模式目前在 `internal/repl/ui.go` 中通过 `handleCommand` 识别
`/help`、`/clear` 和 `/exit`。普通输入直接追加到 `MessageManager.History`，命令解析、
终端输出和 REPL 生命周期集中在同一个文件中。

上下文管理 V2 已设计 `SessionID`、`ConversationStore` 和
`.context/sessions/<session-id>` 本地存储。本设计在该持久化能力之上增加完整的会话
管理命令，不另建一套 transcript 或消息存储格式。

## 2. 目标

- 每次启动交互模式时创建一个新的逻辑会话，而不是自动恢复旧会话；
- 支持新建、列出、恢复、删除、重命名和查看当前会话；
- `/help` 由命令元数据生成，新增命令不再要求手工维护帮助文本；
- 命令解析、会话生命周期和终端展示彼此分离，可独立测试；
- 复用上下文管理 V2 的原始消息、摘要和工具归档；
- 切换会话后，下一次模型请求使用目标会话的上下文；
- 所有破坏性文件操作继续满足会话目录边界和符号链接安全约束。

## 3. 非目标

- 不实现跨设备或跨工作区同步；
- 不实现会话搜索、导出、分叉、自动清理和批量删除；
- 不增加 `/session new` 一类二级命令树；
- 不允许命令在 Agent 正在流式执行时并发修改当前会话；
- 不改变 `/clear` 的含义：它只清屏，不清除上下文；
- 不把自然语言中的 `/` 字符串或非首字符 `/` 解释为命令。

## 4. 方案选择

采用“命令注册表 + 会话服务 + 现有会话存储”的方案。

- 命令注册表负责名称、用法、参数解析和分发；
- 命令处理器负责把命令请求转换为服务调用和用户可读输出；
- `SessionService` 负责当前会话状态及生命周期规则；
- 持久化实现沿用上下文管理 V2 的会话目录和 `ConversationStore`。

相比继续扩展 `handleCommand` 的 `switch`，该方案不会让 `ui.go` 同时承担解析、存储
和状态切换。相比引入 Cobra 等完整命令框架，它更符合 REPL 内少量扁平命令的规模，
也不增加依赖。

## 5. 命令集合与精确语义

### 5.1 `/new [标题]`

创建新的逻辑会话并立即切换过去。标题是可选的，支持空格；解析时去掉首尾空白，
不处理 shell 引号或转义。

- 当前会话有消息时，先确认其原始记录已经成功持久化；失败则保留当前会话并报错；
- 当前会话为空且未落盘时，直接丢弃该空会话；
- 为新会话分配 ID、创建空的 `MessageManager`，并复用当前构建的 System Prompt；
- 新会话在第一条用户消息写入时才创建 manifest，避免空会话污染列表；
- 显式标题只保存在内存中，若会话始终为空，退出时仍不落盘。

成功输出新会话的短 ID 和标题，不自动清屏。

### 5.2 `/sessions`

按 `UpdatedAt` 降序列出当前工作区最近 20 个已持久化会话。每行显示：

```text
* a1b2c3d4  修复登录测试          12 messages  2026-07-22 14:30
  e5f6a7b8  未命名会话             4 messages  2026-07-21 20:10
```

`*` 标记当前会话。尚未持久化的当前空会话额外显示在列表首行。列表为空时输出明确提示。
时间使用本地时区。终端宽度适配不属于首版范围，标题只按 Unicode rune 截断。

### 5.3 `/resume <id>`

保存当前会话并载入目标会话。`id` 可以是完整 ID，也可以是唯一前缀；零匹配和多匹配
均报错且不改变当前状态。

- 不提供 ID 时返回用法错误；
- 目标是当前会话时提示“already active”，不重复加载；
- 先完整读取并校验目标 manifest、transcript 和格式版本；
- 只有目标会话构造成功后才原子替换 REPL 中的当前会话和 `MessageManager`；
- 已保存的 System Prompt 不作为历史恢复，恢复时使用本次进程重新构建的 System Prompt；
- `StoredMessage` 按存储顺序转换为 `message.Message`，工具调用和结果的 ID 必须保持不变；
- 目标会话损坏、版本未知或读取失败时，当前会话保持不变。

成功后显示目标短 ID、标题和消息数，不自动向模型发请求。

### 5.4 `/delete <id>`

删除指定的非当前会话。ID 解析规则与 `/resume` 相同。

- 禁止删除当前会话，用户需先 `/new` 或 `/resume` 到其他会话；
- 展示完整标题和短 ID，并通过终端读取一次 `[y/N]` 确认；
- 只有 `y` 或 `yes`（忽略大小写）执行删除，EOF 和其他输入均取消；
- 删除精确的 `.context/sessions/<resolved-id>` 目录，包括 manifest、transcript、摘要和
  工具归档；
- 删除前再次验证目标规范路径位于会话根目录内，并拒绝符号链接逃逸；
- 删除失败时报告错误，不把部分删除误报为成功。

首版不提供 `--force`，避免脚本或粘贴误删。

### 5.5 `/rename <标题>`

修改当前会话标题。标题不能为空，去除首尾空白后最多 80 个 Unicode rune，超长输入
返回错误而不是静默截断。

当前会话已有消息并已落盘时立即更新 manifest；当前会话尚为空时只更新内存状态。
持久化失败时恢复旧标题。

### 5.6 `/current`

显示当前会话完整 ID、标题、创建时间、更新时间、消息数和持久化状态。该命令只读，
不触发落盘。

### 5.7 `/help [命令]`

无参数时按注册顺序显示所有命令的 usage 和一句说明。提供命令名时显示该命令的完整
usage 和说明；参数可写作 `new` 或 `/new`。未知命令返回错误并提示使用 `/help`。

### 5.8 `/clear` 与 `/exit`

- `/clear`：清理终端并重新输出欢迎信息，不修改消息、会话 ID 或标题；
- `/exit`、`/quit`：确认当前非空会话已经持久化后退出；空会话不落盘；
- `exit` 和 `quit` 继续作为无斜杠退出别名；其他内置命令必须以 `/` 开头，避免普通
  对话文本被误判为命令；
- EOF 与 `/exit` 使用同一退出路径，而不是显示“读取输入失败”。

## 6. 默认标题与会话 ID

新会话初始标题为“未命名会话”。如果用户未通过 `/new [标题]` 或 `/rename` 指定标题，
持久化第一条用户消息时用其第一行生成标题：压缩连续空白，最多保留 40 个 Unicode
rune。无法生成非空标题时继续使用“未命名会话”。标题只用于展示，不参与目录名。

Session ID 是存储层生成的不可变、不透明标识，目录名与完整 ID 相同。命令层不依赖
UUID 的具体格式，仅负责显示前 8 个 rune；ID 不足 8 个 rune 时显示完整值。

## 7. 组件设计

### 7.1 命令注册表

建议在 `internal/repl/command.go` 定义：

```go
type Command struct {
	Name        string
	Usage       string
	Description string
	Run         func(context.Context, *CommandContext, string) CommandResult
}

type CommandRegistry struct {
	ordered []Command
	byName  map[string]Command
}

type CommandResult struct {
	Handled bool
	Quit    bool
	Err     error
}
```

解析规则：对整行先 `TrimSpace`；首字符不是 `/` 时返回 `Handled=false`；命令名取第一个
空白前的内容并转小写；剩余文本整体作为参数交给处理器。未知的 `/name` 必须返回
`Handled=true` 和可读错误，不能转交模型。

注册时拒绝空名称、重复名称和缺失处理器。帮助信息直接读取注册元数据，不维护第二份
命令列表。

### 7.2 会话服务

建议在 `internal/session/service.go` 定义会话生命周期，不让命令处理器直接拼接路径：

```go
type CurrentSession struct {
	ID          string
	Title       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Persisted   bool
	Messages    *message.MessageManager
}

type Service struct {
	store        Store
	systemPrompt string
	current      *CurrentSession
}
```

服务公开 `New`、`List`、`Resume`、`Delete`、`Rename`、`Current`、`AppendUserMessage`
和 `Close`。当前会话指针只由 Service 修改；REPL 通过 `Current().Messages` 获取本轮要
传给 Agent 的消息管理器。

首版 REPL 是串行输入循环，Service 无需自行加锁。若未来允许 Agent 后台执行或多个
UI 客户端共享 Service，再在接口外定义并发语义。

### 7.3 会话存储扩展

命令功能复用上下文管理 V2 的 `ConversationStore`，并在同一 `internal/context` 存储
实现上补足会话目录生命周期：

```go
type SessionMetadata struct {
	ID           string
	Title        string
	Workspace    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int
	FormatVersion int
}

type SessionStore interface {
	ConversationStore
	CreateSession(context.Context, SessionMetadata) error
	GetSession(context.Context, string) (SessionMetadata, error)
	ListSessions(context.Context, string, int) ([]SessionMetadata, error)
	RenameSession(context.Context, string, string) error
	DeleteSession(context.Context, string) error
}
```

`manifest.json` 增加 `title` 和 `message_count`。`workspace` 使用清理后的绝对路径；
`ListSessions` 只返回当前工作区的会话。`AppendMessage` 成功后在同一存储操作中更新
manifest 的 `updated_at` 和 `message_count`；如果不能提供真正事务，顺序应为先追加
transcript，再原子替换 manifest，读取时以 transcript 实际记录数修复落后的计数。

`DeleteSession` 属于 `FileConversationStore` 的受控能力，命令层不得直接调用
`os.RemoveAll`。

### 7.4 REPL 集成

`runInteractive` 初始化顺序调整为：

```text
构建 System Prompt
  -> 初始化 Conversation/Session Store
  -> 创建 SessionService（分配新的逻辑会话）
  -> 初始化 Agent
  -> 创建 CommandRegistry
  -> 进入输入循环
```

普通消息数据流：

```text
用户输入
  -> SessionService.AppendUserMessage
  -> 必要时创建 manifest 并追加原始用户消息
  -> Agent.Run(Current.Messages)
  -> Agent/ContextManager 逐条追加 assistant 与 tool 原始记录
  -> 更新当前会话内存元数据
```

命令数据流：

```text
用户输入
  -> CommandRegistry.Execute
  -> Command Handler
  -> SessionService
  -> SessionStore
  -> 格式化结果
```

命令不会被追加到 transcript，也不会发送给模型。

## 8. 状态切换与一致性

`/resume` 使用“先加载，后切换”：目标会话读取和消息转换全部成功后，才替换当前指针。
`/rename` 使用“先持久化，后改内存”；失败时内存标题不变。`/new` 在确认当前会话无需
补充保存后才分配新状态。

上下文管理 V2 的 Store 是原始事实来源，`MessageManager.History` 只是当前进程缓存。
恢复时从 Store 重建缓存；切换之后不得继续持有旧会话的消息 slice。摘要快照和工具
归档不直接展开进 `History`，下一次请求仍由 `ContextManager.Build` 生成视图。

空会话采用延迟创建：Service 在启动和 `/new` 时分配 ID 与时间戳，但直到第一条用户
消息成功写入前 `Persisted=false`。退出、再次 `/new` 或恢复其他会话时直接丢弃这种
空会话。

## 9. 错误处理

定义可由命令层转成稳定提示的哨兵错误：

- `ErrSessionNotFound`：ID 或前缀无匹配；
- `ErrAmbiguousSessionID`：短 ID 匹配多个会话；
- `ErrCurrentSessionDelete`：试图删除当前会话；
- `ErrInvalidSessionTitle`：标题为空或超过 80 rune；
- `ErrUnsupportedSessionVersion`：manifest 格式未知；
- `ErrCorruptSession`：manifest、transcript 或工具配对损坏。

存储错误必须保留底层错误并包含操作和 Session ID。用户取消删除不是错误。未知命令、
参数缺失和多余参数只打印错误并继续 REPL，不终止进程。

Agent 执行失败后 REPL 继续保留当前会话，不因单次模型或工具错误退出整个交互模式；
已经成功追加的原始记录不回滚。只有初始化失败和无法持久化第一条用户消息这类无法
保证审计的错误才阻止本轮请求。

## 10. 测试策略

### 10.1 命令解析单元测试

- 普通文本、空输入和非首字符 `/` 不作为命令；
- 命令名大小写不敏感，参数保留内部空格；
- 未知斜杠命令被消费并返回错误；
- 重复注册和非法注册失败；
- `/help` 自动包含全部注册命令，单命令帮助接受带或不带 `/` 的名称；
- `exit`、`quit` 仅作为退出命令的兼容别名。

### 10.2 SessionService 单元测试

使用内存 Store 验证：

- 启动产生新的未持久化空会话；
- 第一条用户消息创建会话并生成默认标题；
- 显式标题不被第一条消息覆盖；
- `/new`、`/resume` 切换成功且消息历史不串会话；
- 恢复失败时当前会话不变；
- 完整 ID、唯一前缀、零匹配和多匹配；
- 禁止删除当前会话，删除其他会话需要确认；
- 重命名持久化失败时回滚内存标题；
- 空会话退出不写 Store，非空会话关闭成功；
- 恢复时使用当前 System Prompt，并保持工具调用 ID。

### 10.3 FileConversationStore 测试

- 创建、获取、列出、重命名和删除会话目录；
- 列表只包含当前绝对工作区并按更新时间降序；
- manifest 原子更新和 transcript 领先时的消息计数修复；
- 未知格式版本、损坏 JSONL 和不完整尾记录；
- 路径穿越、符号链接会话目录和错误 Session ID 被拒绝；
- 删除只影响解析后的精确会话目录；
- 目录和文件权限符合上下文管理 V2 约束。

### 10.4 REPL 集成测试

通过注入输入、输出、Agent runner 和临时 Store 验证：

- 每次启动得到不同的新会话，且不自动恢复；
- 命令不进入模型消息历史；
- `/clear` 不改变当前 Session ID 和消息数；
- `/resume` 后下一条消息携带恢复的历史；
- `/delete` 的确认、取消和 EOF 路径；
- `/exit` 和输入 EOF 均执行一致的关闭逻辑；
- Store 或恢复失败时输出错误并保持 REPL 可用。

## 11. 验收标准

1. 每次启动交互模式都分配新的会话，旧会话只通过 `/resume` 恢复；
2. 空会话不会出现在后续启动的 `/sessions` 中；
3. `/new`、`/sessions`、`/resume`、`/delete`、`/rename` 和 `/current` 行为符合本设计；
4. `/help` 完全由注册表生成，帮助列表与实际可用命令一致；
5. 命令不会进入 transcript 或发送给模型；
6. 切换失败不会丢失或替换当前会话；
7. 当前会话不能删除，其他会话删除前必须确认；
8. 恢复后工具调用与结果 ID 保持配对，下一次请求由 `ContextManager` 构建；
9. 会话操作限制在当前工作区的安全会话根目录；
10. `go test ./internal/repl ./internal/session ./internal/context` 通过；
11. `go test ./...` 和 `go vet ./...` 通过。

## 12. 分阶段实施顺序

### 阶段一：命令框架

- 提取命令解析和注册表；
- 迁移现有 `/help`、`/clear`、`/exit`；
- 为 REPL 输入输出增加可测试注入点。

### 阶段二：会话生命周期

- 在上下文管理 V2 的 Store 上补齐会话 metadata 和目录生命周期；
- 实现 `SessionService`、延迟创建和标题生成；
- 接入 `/new`、`/sessions`、`/resume`、`/rename` 和 `/current`。

### 阶段三：安全删除与集成验证

- 实现 Store 层安全删除；
- 接入 `/delete` 的确认流程；
- 完成恢复、切换、EOF、错误路径和全量测试。

## 13. 与上下文管理 V2 的依赖

本设计依赖上下文管理 V2 的阶段一：Session、TurnID、`ConversationStore`、
`FileConversationStore` 和从原始记录恢复消息的能力。若两项工作并行开发，应先稳定
以下共享契约再分别实现：

- `SessionMetadata` 及 manifest 字段；
- `SessionStore` 与 `ConversationStore` 的组合方式；
- `StoredMessage` 到 `message.Message` 的转换规则；
- Session ID 的生成和校验函数；
- `AppendMessage` 更新 manifest 统计信息的一致性规则。

内置命令不依赖卸载、淘汰和摘要阶段完成；这些阶段后续接入时不得改变命令接口。

## 14. 架构决策记录

### ADR-001：使用扁平内置命令

- **状态：** 接受；
- **决定：** 采用 `/new`、`/sessions`、`/resume`、`/delete` 等顶层命令；
- **原因：** 输入短、易发现，符合现有 REPL；
- **代价：** 命令数量增加后可能需要分组展示。

### ADR-002：每次启动创建新会话

- **状态：** 接受；
- **决定：** 不自动恢复最近会话；空会话延迟落盘；
- **原因：** 启动行为明确，避免无意把新任务追加到旧上下文；
- **代价：** 恢复旧任务需要显式执行 `/resume`。

### ADR-003：会话命令复用 Context V2 Store

- **状态：** 接受；
- **决定：** 不创建独立历史数据库或第二套 SessionRepository；
- **原因：** transcript 必须只有一个权威来源，避免恢复和上下文压缩看到不同数据；
- **代价：** 会话命令需等待 Context V2 阶段一的共享接口稳定。

### ADR-004：恢复采用先加载后切换

- **状态：** 接受；
- **决定：** 目标会话完全验证成功后才替换当前状态；
- **原因：** 损坏会话或 I/O 失败不能让正在工作的会话丢失；
- **代价：** 恢复期间会短暂同时占用新旧消息的内存。

### ADR-005：禁止删除当前会话

- **状态：** 接受；
- **决定：** 当前会话必须先切走才能删除；
- **原因：** 避免内存仍引用已删除的 transcript 和归档；
- **代价：** 删除当前会话需要额外执行一次 `/new`。
