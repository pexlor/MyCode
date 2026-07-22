# 上下文管理 V2 实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [x]`）语法来跟踪进度。

**目标：** 为 MyCode 实现本地持久化、按预算触发、不会重复压缩已覆盖消息的四级上下文管理。

**架构：** `ConversationStore` 保存完整 transcript、工具 Artifact 和 active summary 检查点；`ContextManager` 每次从 active summary 的覆盖游标之后读取消息并构造 `ContextView`。按需加载每次装配，工具卸载在新结果超限时触发，旧结果淘汰在工具预算超限时触发，会话摘要只在整体达到软阈值且存在新增完整 Turn 时触发。

**技术栈：** Go 1.24、标准库 JSON/文件系统/SHA256、现有 OpenAI 兼容 LLMClient、Go testing。

---

## 文件结构

- 创建 `internal/context/types.go`：Session、StoredMessage、Artifact、Summary、ContextView 类型。
- 创建 `internal/context/store.go`：ConversationStore 接口。
- 创建 `internal/context/file_store.go`：JSONL、Artifact、Summary 和原子 manifest 实现。
- 创建 `internal/context/file_store_test.go`：持久化、检查点、崩溃恢复测试。
- 创建 `internal/context/budget.go`：预算计算与验证。
- 创建 `internal/context/estimator.go`：保守 Token 估算。
- 创建 `internal/context/budget_test.go`：预算和估算测试。
- 创建 `internal/context/offloader.go`：大工具结果归档和引用渲染。
- 创建 `internal/context/offloader_test.go`：单项/批次归档及失败测试。
- 创建 `internal/context/evictor.go`：旧工具结果确定性淘汰。
- 创建 `internal/context/evictor_test.go`：重复工具结果与协议配对测试。
- 创建 `internal/context/loader.go`：规则加载与工具 schema 选择。
- 创建 `internal/context/loader_test.go`：路径规则和工具分组测试。
- 创建 `internal/context/compactor.go`：增量摘要、覆盖游标和回退。
- 创建 `internal/context/compactor_test.go`：增量范围、无新增量、防抖和回退测试。
- 创建 `internal/context/manager.go`：四级触发式编排。
- 创建 `internal/context/manager_test.go`：ContextView 端到端构建测试。
- 修改 `internal/tool/tools_manager.go`：支持按名称构建 schema。
- 修改 `internal/tool/tools_manager_test.go`：验证 schema 子集。
- 修改 `internal/agent/agent.go`：写 Store 并从 ContextManager 获取模型请求。
- 修改 `internal/agent/agent_test.go`：验证 Agent 不重复发送已压缩历史。
- 修改 `internal/repl/ui.go`：初始化 Session、Store、ContextManager 和摘要客户端。
- 修改 `.gitignore`：忽略 `.context/`。

### 任务 1：建立会话存储和压缩检查点

**文件：**
- 创建：`internal/context/types.go`
- 创建：`internal/context/store.go`
- 创建：`internal/context/file_store.go`
- 测试：`internal/context/file_store_test.go`

- [x] **步骤 1：编写失败的检查点测试**

测试创建 Turn 1～3，提交覆盖 Turn 1～2 的 V1，然后断言：

```go
active, err := store.ActiveSummary(ctx, sessionID)
if err != nil || active.CoveredThroughMessageID != "m2" {
	t.Fatalf("active summary = %#v, err = %v", active, err)
}
messages, err := store.ListMessagesAfter(ctx, sessionID, active.CoveredThroughMessageID)
if err != nil || len(messages) != 1 || messages[0].ID != "m3" {
	t.Fatalf("messages after checkpoint = %#v, err = %v", messages, err)
}
```

同时测试 `CommitSummary(snapshot, expectedVersion)` 拒绝错误版本，且未激活摘要文件不会改变 active summary。

- [x] **步骤 2：运行测试验证失败**

运行：`go test ./internal/context -run 'TestFileStore|TestSummaryCheckpoint'`

预期：FAIL，缺少 Store 类型或方法。

- [x] **步骤 3：实现最小文件存储**

实现接口：

```go
type ConversationStore interface {
	AppendMessage(context.Context, StoredMessage) error
	ListMessages(context.Context, string) ([]StoredMessage, error)
	ListMessagesAfter(context.Context, string, string) ([]StoredMessage, error)
	SaveToolArtifact(context.Context, ToolArtifact, io.Reader) error
	LoadToolArtifact(context.Context, string, string) (ToolArtifact, io.ReadCloser, error)
	ActiveSummary(context.Context, string) (*SummarySnapshot, error)
	CommitSummary(context.Context, SummarySnapshot, int) error
}
```

所有会话 ID 和 Artifact ID 只允许字母、数字、点、下划线和短横线；目录 `0700`、文件 `0600`。Summary 先原子写正文和元数据，再通过原子重命名 manifest 激活。

- [x] **步骤 4：运行存储测试**

运行：`go test ./internal/context -run 'TestFileStore|TestSummaryCheckpoint'`

预期：PASS。

- [x] **步骤 5：提交**

```bash
git add internal/context/types.go internal/context/store.go internal/context/file_store.go internal/context/file_store_test.go
git commit -m "feat: add context session store"
```

### 任务 2：实现 Token 预算和保守估算

**文件：**
- 创建：`internal/context/budget.go`
- 创建：`internal/context/estimator.go`
- 测试：`internal/context/budget_test.go`

- [x] **步骤 1：编写失败的预算测试**

```go
func TestNewBudgetReservesOutputToolsAndMargin(t *testing.T) {
	b, err := NewBudget(ModelContextSpec{ContextWindow: 100_000, MaxOutputTokens: 10_000}, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if b.HardInputLimit != 75_000 || b.SoftCompactLimit != 56_250 || b.ToolHistoryLimit != 18_750 {
		t.Fatalf("unexpected budget: %#v", b)
	}
}
```

增加非法窗口、非法比例、中文和工具 JSON 估算测试。

- [x] **步骤 2：运行测试验证失败**

运行：`go test ./internal/context -run 'TestNewBudget|TestConservativeEstimator'`

预期：FAIL，缺少预算和估算器。

- [x] **步骤 3：实现预算与估算器**

```go
hard := window - outputReserve - toolReserve - safetyMargin
soft := int(float64(hard) * policy.SoftCompactRatio)
toolHistory := int(float64(hard) * policy.ToolHistoryRatio)
```

Fallback 估算使用 `ceil(utf8Bytes/3*1.15)`，消息额外加入固定包装开销，工具 schema 以 JSON 编码后估算。

- [x] **步骤 4：运行预算测试**

运行：`go test ./internal/context -run 'TestNewBudget|TestConservativeEstimator'`

预期：PASS。

- [x] **步骤 5：提交**

```bash
git add internal/context/budget.go internal/context/estimator.go internal/context/budget_test.go
git commit -m "feat: add context token budgeting"
```

### 任务 3：实现工具结果卸载与淘汰

**文件：**
- 创建：`internal/context/offloader.go`
- 创建：`internal/context/offloader_test.go`
- 创建：`internal/context/evictor.go`
- 创建：`internal/context/evictor_test.go`

- [x] **步骤 1：编写失败的归档测试**

构造超过阈值的结果，断言完整正文可从 Artifact 恢复，而 ContextView 中只保留引用：

```go
got, err := offloader.Process(ctx, sessionID, []StoredMessage{toolMessage})
if err != nil {
	t.Fatal(err)
}
if strings.Contains(got[0].ToolResults[0].Content, largeOutput) {
	t.Fatal("full output leaked into context view")
}
if got[0].ToolResults[0].ArtifactID == "" {
	t.Fatal("missing artifact reference")
}
```

增加归档失败不替换原文、当前 Turn 极大结果仍归档的测试。

- [x] **步骤 2：编写失败的淘汰测试**

连续三次执行相同测试命令，断言只保留最新完整结果；旧结果保留原 `ToolUseID` 的短引用。当前 Turn 的结果不能变为 `DROPPED`。

- [x] **步骤 3：运行测试验证失败**

运行：`go test ./internal/context -run 'TestOffloader|TestEvictor'`

预期：FAIL，缺少处理器。

- [x] **步骤 4：实现卸载和确定性淘汰**

卸载器计算单项和批次 Token，使用 SHA256、确定性首尾预览和 Store 写入。淘汰器按“相同工具+规范化参数”的 key 判断覆盖关系，按旧到新将 `FULL` 降级为 `REFERENCE`，直到工具历史回到预算。

- [x] **步骤 5：运行工具治理测试**

运行：`go test ./internal/context -run 'TestOffloader|TestEvictor'`

预期：PASS。

- [x] **步骤 6：提交**

```bash
git add internal/context/offloader.go internal/context/offloader_test.go internal/context/evictor.go internal/context/evictor_test.go
git commit -m "feat: manage tool result context"
```

### 任务 4：实现按需规则和工具加载

**文件：**
- 创建：`internal/context/loader.go`
- 创建：`internal/context/loader_test.go`
- 修改：`internal/tool/tools_manager.go`
- 修改：`internal/tool/tools_manager_test.go`

- [x] **步骤 1：编写失败的路径规则测试**

在临时工作区创建根 `.agent/context.md` 和 `internal/agent/.agent/context.md`，活跃路径为 `internal/agent/agent.go`，断言两份规则按根到叶顺序加载且不能越出工作区。

- [x] **步骤 2：编写失败的工具子集测试**

```go
schemas := manager.BuildSchemas([]string{"ReadFile", "Grep"})
if len(schemas) != 2 {
	t.Fatalf("schemas = %d, want 2", len(schemas))
}
```

验证未知名称返回错误，不能静默丢失请求能力。

- [x] **步骤 3：运行测试验证失败**

运行：`go test ./internal/context ./internal/tool -run 'TestDemandLoader|TestBuildSchemas'`

预期：FAIL，缺少 Loader 和 schema 子集方法。

- [x] **步骤 4：实现确定性加载器**

实现根规则、活跃路径规则和工具分组。无法判断意图时返回全部工具 schema；选择 schema 只影响模型可见工具，不绕过 `ToolsManager.Execute` 权限检查。

- [x] **步骤 5：运行加载测试**

运行：`go test ./internal/context ./internal/tool -run 'TestDemandLoader|TestBuildSchemas'`

预期：PASS。

- [x] **步骤 6：提交**

```bash
git add internal/context/loader.go internal/context/loader_test.go internal/tool/tools_manager.go internal/tool/tools_manager_test.go
git commit -m "feat: load context and tools on demand"
```

### 任务 5：实现增量摘要与检查点提交

**文件：**
- 创建：`internal/context/compactor.go`
- 创建：`internal/context/compactor_test.go`

- [x] **步骤 1：编写失败的增量压缩测试**

Fake Summarizer 记录请求。V1 覆盖 `m15`，Store 包含 `m1`～`m24`，断言请求只包含 `m16`～本次结束点，不包含 `m1`～`m15`；提交后 V2 游标单调前进。

- [x] **步骤 2：编写无新增量和回退测试**

没有覆盖游标后的可压缩 Turn 时，Fake Summarizer 调用次数必须为 0。主摘要器失败时调用 fallback 一次；两者失败时生成固定结构的最小任务状态。

- [x] **步骤 3：运行测试验证失败**

运行：`go test ./internal/context -run 'TestCompactor'`

预期：FAIL，缺少 Compactor。

- [x] **步骤 4：实现增量 Compactor**

```go
type Summarizer interface {
	Summarize(context.Context, SummarizeRequest) (SummarizeResponse, error)
}
```

Compactor 只接收 active summary 加游标之后的消息，保留最近完整 Turn，检查最小增量，验证摘要非空且不超预算，再调用 `CommitSummary`。同一次压缩最多主模型和 fallback 各一次。

- [x] **步骤 5：运行摘要测试**

运行：`go test ./internal/context -run 'TestCompactor'`

预期：PASS。

- [x] **步骤 6：提交**

```bash
git add internal/context/compactor.go internal/context/compactor_test.go
git commit -m "feat: add incremental context compaction"
```

### 任务 6：编排 ContextManager 并接入 Agent

**文件：**
- 创建：`internal/context/manager.go`
- 创建：`internal/context/manager_test.go`
- 修改：`internal/agent/agent.go`
- 修改：`internal/agent/agent_test.go`
- 修改：`internal/repl/ui.go`
- 修改：`.gitignore`

- [x] **步骤 1：编写失败的 ContextManager 测试**

断言 Build 的输入为 active summary 加游标之后的消息；低于所有阈值时不调用 Offloader、Evictor 或 Summarizer；超过各自阈值时只触发对应组件；最终超过硬限制返回 `ErrContextBudgetExceeded`。

- [x] **步骤 2：编写失败的 Agent 集成测试**

Fake LLM 捕获两次 Stream 请求。第一次压缩并激活 V1 后，第二次请求不得包含已覆盖消息正文，只包含 V1 和游标之后的消息；tool call/result ID 始终配对。

- [x] **步骤 3：运行测试验证失败**

运行：`go test ./internal/context ./internal/agent -run 'TestContextManager|TestAgentUsesContextView'`

预期：FAIL，ContextManager 尚未接入。

- [x] **步骤 4：实现触发式编排和 Agent 注入**

`NewAgent` 增加可选 `ContextManager` 依赖；未配置时保留现有行为以兼容测试。配置后每次 Stream 前调用 Build，工具结果先追加 Store，再由下一次 Build 处理。

- [x] **步骤 5：初始化 CLI 会话**

REPL 启动时生成 SessionID，创建 `.context/sessions` Store、默认 Policy 和可选 Summary Model；凭据从环境变量加载，不在新增配置或日志中写明文密钥。将 `.context/` 加入 `.gitignore`。

- [x] **步骤 6：运行集成测试**

运行：`go test ./internal/context ./internal/agent ./internal/repl`

预期：PASS。

- [x] **步骤 7：运行完整测试和静态检查**

运行：`gofmt -w internal/context internal/tool/tools_manager.go internal/tool/tools_manager_test.go internal/agent/agent.go internal/agent/agent_test.go internal/repl/ui.go`

运行：`go test ./...`

运行：`go vet ./...`

预期：全部通过。

- [x] **步骤 8：提交**

```bash
git add .gitignore internal/context internal/tool/tools_manager.go internal/tool/tools_manager_test.go internal/agent/agent.go internal/agent/agent_test.go internal/repl/ui.go
git commit -m "feat: integrate context management into agent"
```
