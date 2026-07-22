# 跨项目用户长期记忆设计

## 1. 背景

MyCode 当前的系统提示词只包含静态规则和运行环境。新的会话不会记得用户在其他代码
项目中已经确认过的开发偏好、工作习惯和技术背景，因此用户需要反复说明测试方式、代码
风格、协作习惯和常用工具。

本设计为 MyCode 增加一套仅服务代码工作的用户级长期记忆。系统可以在对话中发现候选
记忆，但任何候选都必须经过用户确认才能进入正式记忆。正式记忆采用本地分层 Markdown
保存，以便用户直接阅读、编辑、备份和审计；MVP 不引入数据库、向量索引或云端服务。

## 2. 目标与非目标

### 2.1 目标

- 在不同代码项目和不同会话之间复用同一份用户偏好；
- 自动识别稳定、跨项目且对编码任务有帮助的信息；
- 在写入前展示候选内容、分类、适用范围和识别依据；
- 只有用户明确确认后才写入正式记忆；
- 每次请求只加载与当前任务相关的记忆，并控制注入预算；
- 支持查看、修改、遗忘和禁用记忆；
- 所有数据只保存在本机，使用文本格式并支持人工审计；
- 单个组件职责清晰，候选识别、存储、召回和提示词注入可以独立测试。

### 2.2 非目标

- 不记录生活经历、私人关系或与代码工作无关的个人资料；
- 不保存 API Key、密码、Token、Cookie、私钥等秘密；
- 不保存某个仓库独有的架构、命令、路径或任务进度，这些内容应留在项目文档或
  `AGENTS.md`；
- 不自动批准候选记忆，也不根据沉默推断同意；
- 不同步到云端，不做跨设备共享；
- MVP 不实现 embedding、向量数据库、自动过期删除或模型训练；
- 不把完整对话原文作为长期记忆保存。

## 3. 核心原则

### 3.1 候选与正式记忆分离

模型只能提出候选，不能直接修改正式记忆。候选进入待确认队列后，由用户执行确认、修改
后确认或拒绝。拒绝和未处理候选都不能参与后续召回。

### 3.2 用户事实与项目事实分离

长期记忆只回答“用户通常如何工作”。项目文档回答“这个仓库如何工作”。当一条信息同时
包含用户偏好和项目细节时，只提取其中可跨项目复用的偏好，项目部分不进入用户记忆。

### 3.3 原始存储与提示词视图分离

磁盘上的 Markdown 是可审计的事实源。注入模型的 `MemoryView` 是按当前任务生成的临时
视图，可以过滤、排序和截断，但不能反向修改事实源。

### 3.4 最小化与可撤销

只保存未来可能改变 Agent 行为的信息。每条记忆都必须有稳定 ID、确认时间和适用范围，
并能通过命令删除。删除后不得保留隐藏副本或继续注入。

## 4. 总体架构

```text
用户输入
  -> Agent 执行代码任务
  -> CandidateDetector 分析本轮公开对话
  -> CandidatePolicy 过滤秘密、项目事实和低价值信息
  -> CandidateQueue 保存待确认候选
  -> REPL 展示候选并请求用户确认
       -> 确认：MemoryStore 写入正式 Markdown
       -> 修改：按修改后的内容写入
       -> 拒绝：从队列移除，不进入正式记忆

新会话或新请求
  -> TaskClassifier 提取当前任务主题
  -> MemoryRetriever 从索引选择相关正式记忆
  -> MemoryBudgeter 去重、排序和截断
  -> PromptInjector 注入系统提示词
  -> Agent 执行任务
```

组件边界：

- `MemoryStore` 只负责读取、写入、更新、删除和维护索引；
- `CandidateDetector` 只从一轮对话生成结构化候选，不接触磁盘；
- `CandidatePolicy` 只做资格、隐私和重复性校验；
- `CandidateQueue` 只管理待确认状态；
- `MemoryRetriever` 只从已确认记忆生成候选集合；
- `MemoryBudgeter` 只负责相关性排序和 Token 预算；
- REPL 负责与用户确认，不把交互逻辑放入存储层；
- `prompt.BuildSystemPrompt` 只接收最终 `MemoryView` 并完成注入。

## 5. 存储结构

默认目录为 `~/.ffcode/memory/`。它属于 MyCode/FFCode，不与通用聊天助手共享，因此满足
“只在代码项目中使用”的范围限制。根目录可通过配置覆盖，但覆盖路径必须是绝对路径。

```text
~/.ffcode/memory/
├── config.md
├── INDEX.md
├── profile.md
├── preferences/
│   ├── INDEX.md
│   ├── coding-style.md
│   ├── testing.md
│   ├── git-workflow.md
│   └── tooling.md
├── workflows/
│   ├── INDEX.md
│   └── collaboration.md
└── candidates/
    └── pending.jsonl
```

正式记忆使用 Markdown，待确认队列使用 JSONL。JSONL 便于保存状态机字段，且不会被误当成
正式记忆注入模型。`candidates/` 中的内容永远不参与召回。

每个分类必须有 `INDEX.md`。索引超过 100 条记录时按主题拆分子分类；MVP 不自动拆分，
只在诊断命令中提示维护建议。

## 6. 数据模型

### 6.1 正式记忆

```go
type Memory struct {
	ID          string
	Category    MemoryCategory
	Content     string
	Scope       MemoryScope
	Tags        []string
	Source      MemorySource
	CandidateID string
	ConfirmedAt time.Time
	UpdatedAt   time.Time
}

type MemoryScope string

const (
	ScopeAllCodeProjects MemoryScope = "all-code-projects"
	ScopeLanguage        MemoryScope = "language"
	ScopeTaskType        MemoryScope = "task-type"
)
```

Markdown 中每条记忆使用稳定、可人工编辑的记录块：

```markdown
### mem_01J...

- 内容：修改行为前优先补充或更新测试。
- 范围：all-code-projects
- 标签：testing, workflow
- 来源：用户于 2026-07-22 确认的候选
- 确认时间：2026-07-22T15:30:00+08:00
- 更新时间：2026-07-22T15:30:00+08:00
```

`Source` 只记录可审计的简短依据，不复制整段对话，也不保存隐藏思考。
`CandidateID` 保存产生该记忆的候选 ID；手工创建的记忆为空。确认重试时先通过该字段检查是否
已经写入，避免进程在“正式记忆已写入、候选状态未更新”之间崩溃后生成重复记录。

### 6.2 候选记忆

```go
type MemoryCandidate struct {
	ID           string
	Content      string
	Category     MemoryCategory
	Scope        MemoryScope
	Tags         []string
	Evidence     string
	Confidence   float64
	Status       CandidateStatus
	CreatedAt    time.Time
	ResolvedAt   *time.Time
}
```

候选状态只能按以下方向转换：

```text
pending -> confirmed
pending -> edited-and-confirmed
pending -> rejected
pending -> expired
```

终态不可再次转换。候选确认后先原子写入正式文件并更新索引，全部成功后才把候选标记为
`confirmed`；写入失败时保持 `pending`，允许重试。

## 7. 候选识别规则

### 7.1 可以提出候选的信息

- 稳定偏好：代码风格、测试策略、提交习惯、文档语言；
- 工作习惯：先设计再实现、验证方式、反馈粒度、是否允许自动提交；
- 技术背景：熟悉的语言、框架和工具，但不推断能力等级；
- 跨项目约束：例如默认不引入新依赖、优先保持向后兼容；
- 用户明确使用“以后”“通常”“默认”“我习惯”等表达的信息。

### 7.2 不得提出候选的信息

- 密钥、认证信息和疑似凭据；
- 身份证号、住址、健康、财务等高敏感信息；
- 当前任务的一次性要求；
- 仓库路径、内部服务地址、分支名、具体缺陷和未完成任务；
- 从一次行为中推断出的性格、能力或偏好；
- 与已有记忆语义相同的重复内容；
- 与现有记忆冲突但未明确说明“以后改成”的信息。

### 7.3 提议时机

候选检测在一轮任务产生最终回复后运行，不阻塞本轮代码执行。每轮最多展示两个候选，超出
部分保留在待确认队列。候选必须来自用户公开表达或用户批准的规格，不能来自模型隐藏思考、
工具输出或第三方文件中的描述。

置信度仅用于排序，不用于自动确认。即使置信度为 1，也必须由用户确认。

## 8. 确认交互

MVP 在 REPL 最终回复之后显示紧凑提示：

```text
memory candidate mem_c_01J...
  content: 修改行为前优先补充或更新测试
  category: preferences/testing
  scope: all-code-projects
  reason: 你说“以后修 bug 先写失败测试”
confirm? [y]es / [e]dit / [n]o / [l]ater
```

- `yes`：写入正式记忆；
- `edit`：打开单行编辑，重新执行秘密检测和重复检测后写入；
- `no`：标记为拒绝；
- `later`：保持待确认，本次不再询问。

候选确认是 REPL 的显式状态，不能把 `y`、`n` 等输入追加到正常对话历史。用户也可以使用
命令集中管理：

```text
/memory list
/memory pending
/memory confirm <candidate-id>
/memory reject <candidate-id>
/memory edit <memory-id>
/memory forget <memory-id>
/memory off
/memory on
```

`forget` 操作显示将删除的完整记忆并再次确认。删除正式记录与更新索引必须组成一个原子逻辑
操作；失败时恢复原文件。

## 9. 召回与提示词注入

### 9.1 召回流程

1. 根据当前用户请求、工作目录和可用语言识别任务标签；
2. 总是考虑 `profile.md`，但只选择与当前任务有关的条目；
3. 通过 `INDEX.md` 的分类、范围和标签筛选正式记忆；
4. 按范围匹配、标签匹配、更新时间和通用优先级排序；
5. 对内容归一化后去重；
6. 在预算内生成 `MemoryView`。

MVP 采用确定性标签检索，不调用额外模型，不做全库语义搜索。无法识别任务标签时，只注入
少量 `all-code-projects` 记忆，不扫描并注入全部内容。

### 9.2 注入预算

默认最多注入 12 条、600 Token，两个限制任一先达到即停止。单条记忆超过 100 Token 时只
注入内容字段，不注入来源元数据。预算和条数可配置，但必须设置硬上限。

注入格式：

```markdown
# Confirmed User Preferences

The following memories were explicitly confirmed by the user. Apply them only
when relevant to the current coding task. Direct instructions in the current
conversation and repository AGENTS.md take precedence.

- [mem_01J...] 修改行为前优先补充或更新测试。
```

优先级固定为：当前系统与开发规则 > 当前用户指令 > 当前目录适用的 `AGENTS.md` > 已确认
用户记忆 > Agent 默认行为。记忆与更高优先级规则冲突时跳过，不尝试改写记忆。

## 10. 配置

`config.md` 保存人类可读设置：

```markdown
# Memory Configuration

- enabled: true
- candidate_detection: true
- confirmation_mode: prompt
- max_injected_items: 12
- max_injected_tokens: 600
- pending_expiry_days: 30
```

程序读取配置时使用严格白名单。未知字段给出警告但不阻止启动；类型或范围错误则回退到默认
值并显示诊断信息。`enabled: false` 时不检测候选、不召回、不注入，但保留磁盘数据。

## 11. 一致性、并发与故障处理

- 首次使用时以临时目录生成完整结构，再原子重命名为正式目录；
- 正式 Markdown 与索引更新采用同目录临时文件加原子重命名；
- `pending.jsonl` 采用追加事件：每次状态变化追加完整新版本，读取时以同一候选 ID 的最后
  一条合法记录为准，避免原地修改损坏整个队列；
- 使用锁文件防止两个 MyCode 进程同时修改记忆库；
- 读取期间遇到单条格式错误时跳过该条并报告文件和记忆 ID，不猜测内容；
- 索引缺失或与正文不一致时，以正文为事实源重建索引；
- 候选检测失败不能影响正常编码任务，只记录可见警告；
- 召回失败时不注入记忆并继续任务，不使用过期缓存伪装成功；
- 存储根目录不得跟随指向目录外的符号链接，写入路径必须经过清理和边界校验。

## 12. 隐私与安全

- 数据默认仅保存在 `~/.ffcode/memory/`，目录权限设为仅当前用户可读写；
- 秘密检测同时检查候选内容、用户编辑内容和导入内容；
- 检测到疑似秘密时拒绝创建候选，并明确说明长期记忆不会保存凭据；
- 不保存完整 prompt、工具输出、代码文件或对话 transcript；
- `Source` 中的依据限制长度并移除绝对路径、URL 查询参数和疑似凭据；
- 用户可以直接编辑 Markdown，但下次加载仍需执行格式与秘密校验；
- 诊断日志只记录记忆 ID、状态和错误，不输出记忆正文。

## 13. 与现有代码的接入点

- 新建 `internal/memory/`，承载模型、存储、策略、召回和预算组件；
- `internal/prompt.BuildSystemPrompt` 接收 `MemoryView`，在静态提示词与环境信息之后追加
  已确认偏好；
- `internal/repl` 在初始化时创建记忆服务，在正常回复结束后展示候选确认；
- `internal/agent` 通过事件返回本轮最终公开文本和候选，不负责终端交互；
- `internal/message` 不保存确认输入，避免把记忆控制命令污染为用户对话；
- `/memory` 子命令由 REPL 路由到记忆服务，不允许模型通过普通工具绕过确认流程写入。

候选检测在主任务完成后发起一次独立模型请求。输入只包含本轮用户公开消息、最终回复和已有
记忆的 ID/内容摘要，不包含隐藏思考和原始工具输出。MVP 复用当前主模型，并要求通过结构化
schema 返回；候选结果不追加到正常对话历史。解析失败等价于本轮没有候选。更换独立小模型
属于后续优化，不改变 `CandidateDetector` 接口。

## 14. 测试策略

### 14.1 单元测试

- 候选资格：稳定偏好被接受，一次性要求、项目事实和秘密被拒绝；
- 状态机：只允许定义的状态转换，重复确认保持幂等；
- 存储：原子写入、索引更新、损坏恢复和并发锁；
- 去重：完全重复和归一化后的重复不会生成新候选；
- 召回：范围、标签、优先级和预算边界符合预期；
- 注入：只有 confirmed 记忆进入提示词，pending/rejected 永不注入；
- 配置：无效值安全回退，关闭开关后不读写记忆内容。

### 14.2 集成测试

- 在项目 A 确认测试偏好，启动项目 B 后能召回该偏好；
- 在项目 A 拒绝候选，项目 B 不出现该内容；
- 修改并确认候选后只保存修改后的版本；
- 忘记记忆后，新会话和当前会话的下一次请求都不再注入；
- 两个进程同时确认不同候选时不丢失记录或索引；
- 存储目录不可写、索引损坏或检测模型失败时，编码任务仍可继续。

### 14.3 安全测试

- 常见 API Key、私钥、Bearer Token 和连接串不会进入候选或正式记忆；
- `../`、绝对路径和符号链接不能使写入逃逸记忆根目录；
- 手工篡改的 Markdown 不能通过内容注入覆盖系统规则；
- 超长候选、超大索引和畸形 JSONL 不会导致无限内存使用。

## 15. 分阶段交付

### 阶段一：可控存储

实现目录初始化、Markdown 存储、索引、`/memory list|edit|forget|on|off` 和静态召回。此阶段
通过命令手工新增测试数据，验证跨项目加载和优先级。

### 阶段二：候选确认

实现结构化候选检测、策略过滤、待确认队列和 REPL 确认状态机。默认每轮最多展示两个候选。

### 阶段三：相关召回

实现任务标签、范围匹配、预算器和诊断输出，减少无关记忆进入上下文。

向量检索只有在正式记忆超过 500 条且标签召回效果不足时才重新评估，不作为当前路线图的
默认步骤。

## 16. 验收标准

- 用户在任意代码项目中确认一条偏好后，另一项目的新会话可以按需召回；
- 未确认、已拒绝和已过期候选在任何情况下都不会进入系统提示词；
- 用户可以查看每条记忆的内容、来源、范围和确认时间；
- 用户可以修改或彻底忘记一条记忆，后续请求立即生效；
- 系统不会保存测试用的秘密样本或项目专属信息；
- 默认注入不超过 12 条和 600 Token；
- 记忆服务不可用时，MyCode 仍能完成普通编码会话；
- 所有核心状态转换、存储边界和召回预算均有自动化测试覆盖。
