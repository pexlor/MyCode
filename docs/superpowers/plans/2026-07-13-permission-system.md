# 权限系统实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 实现按工具名分类的权限策略，并确保 Agent 在执行任何工具之前完成自动允许、绝对拒绝或用户审批。

**架构：** `internal/permission` 提供不依赖 Agent 的策略检查器、请求结构和审批函数类型。Agent 持有检查器与可选审批器，在工具查找和执行前统一应用策略，并通过 AgentEvent 暴露审批请求与结果。现有构造函数保持不变，命令行入口显式允许当前只读工具。

**技术栈：** Go 1.24、标准库 `context`/`errors`/`fmt`/`testing`

---

## 文件结构

- 创建 `internal/permission/permission_test.go`：权限等级、默认策略、输入验证和防御性复制测试。
- 修改 `internal/permission/permission.go`：权限等级、请求、审批器和 Checker 实现。
- 创建 `internal/agent/permission_test.go`：Agent 权限门控和权限事件测试。
- 修改 `internal/agent/agent.go`：保存权限配置并在工具执行前应用策略。
- 修改 `internal/agent/events.go`：定义权限请求与权限决定事件。
- 修改 `internal/repl/ui.go`：应用启动时显式允许当前 `ReadFile` 工具。

### 任务 1：实现独立权限策略包

**文件：**
- 创建：`internal/permission/permission_test.go`
- 修改：`internal/permission/permission.go`

- [ ] **步骤 1：编写 Checker 的失败测试**

测试覆盖显式三种等级、未知工具默认 Ask、无效等级，以及输入 map 的防御性复制：

```go
func TestCheckerCheck(t *testing.T) {
    tests := []struct {
        name string
        rules map[string]Level
        tool string
        want Level
    }{
        {name: "allow", rules: map[string]Level{"ReadFile": Allow}, tool: "ReadFile", want: Allow},
        {name: "ask", rules: map[string]Level{"WriteFile": Ask}, tool: "WriteFile", want: Ask},
        {name: "deny", rules: map[string]Level{"Shell": Deny}, tool: "Shell", want: Deny},
        {name: "unknown defaults to ask", rules: nil, tool: "FutureTool", want: Ask},
    }
    // 对每项构造 Checker 并断言 Check 返回 want。
}

func TestNewCheckerRejectsInvalidLevel(t *testing.T) {
    if _, err := NewChecker(map[string]Level{"tool": Level(99)}); err == nil {
        t.Fatal("NewChecker() error = nil, want invalid level error")
    }
}

func TestNewCheckerCopiesRules(t *testing.T) {
    rules := map[string]Level{"ReadFile": Allow}
    checker, err := NewChecker(rules)
    if err != nil { t.Fatal(err) }
    rules["ReadFile"] = Deny
    if got := checker.Check("ReadFile"); got != Allow {
        t.Fatalf("Check() = %v, want %v", got, Allow)
    }
}
```

- [ ] **步骤 2：运行权限包测试并确认失败**

运行：`go test ./internal/permission`

预期：FAIL，提示 `Level`、`NewChecker` 等标识符不存在。

- [ ] **步骤 3：编写最少权限包实现**

```go
type Level uint8

const (
    Allow Level = iota
    Ask
    Deny
)

type Request struct {
    ToolCallID string
    ToolName   string
    Arguments  map[string]any
}

type Approver func(context.Context, Request) (bool, error)
type Checker struct { rules map[string]Level }

func NewChecker(rules map[string]Level) (*Checker, error)
func (c *Checker) Check(toolName string) Level
```

`NewChecker` 复制规则 map，并拒绝不属于三个常量的值；nil Checker 的 `Check` 安全返回 Ask。

- [ ] **步骤 4：格式化并运行权限包测试**

运行：`gofmt -w internal/permission/permission.go internal/permission/permission_test.go && go test ./internal/permission`

预期：PASS。

- [ ] **步骤 5：提交权限策略包**

```bash
git add internal/permission/permission.go internal/permission/permission_test.go
git commit -m "feat: add tool permission policy"
```

### 任务 2：在 Agent 工具执行路径中实施 Allow 和 Deny

**文件：**
- 创建：`internal/agent/permission_test.go`
- 修改：`internal/agent/agent.go`
- 修改：`internal/agent/events.go`

- [ ] **步骤 1：编写自动允许和绝对拒绝的失败测试**

创建实现 `tool.Tool` 的 `countingTool`，其 `Execute` 增加计数。测试：

```go
func TestExecuteToolAllowedWithoutApproval(t *testing.T)
func TestExecuteToolDeniedWithoutApproval(t *testing.T)
```

允许规则应执行一次且审批次数为零；拒绝规则应返回 `IsError`，执行次数和审批次数均为零。通过 `SetPermissionChecker` 与 `SetPermissionApprover` 配置 Agent。

- [ ] **步骤 2：运行测试并确认编译失败**

运行：`go test ./internal/agent -run 'TestExecuteTool(Allowed|Denied)'`

预期：FAIL，提示权限配置方法不存在。

- [ ] **步骤 3：添加配置、事件和基础门控**

给 `Agent` 添加：

```go
permissionChecker  *permission.Checker
permissionApprover permission.Approver

func (a *Agent) SetPermissionChecker(checker *permission.Checker)
func (a *Agent) SetPermissionApprover(approver permission.Approver)
```

给 `events.go` 添加并实现 `agentEvent()`：

```go
type PermissionRequestEvent struct {
    ToolUseID string
    ToolName string
    Arguments map[string]any
}

type PermissionDecisionEvent struct {
    ToolUseID string
    ToolName string
    Granted bool
    Err error
}
```

将 `executeTool` 改为接收事件 channel，并在工具查找/执行前检查规则。nil checker 等价于 Ask；Deny 返回 `permission denied for tool %q`。

- [ ] **步骤 4：格式化并验证通过**

运行：`gofmt -w internal/agent/agent.go internal/agent/events.go internal/agent/permission_test.go && go test ./internal/agent -run 'TestExecuteTool(Allowed|Denied)'`

预期：PASS。

- [ ] **步骤 5：提交基础门控**

```bash
git add internal/agent/agent.go internal/agent/events.go internal/agent/permission_test.go
git commit -m "feat: enforce allow and deny tool permissions"
```

### 任务 3：实现 Ask 审批流程及事件

**文件：**
- 修改：`internal/agent/permission_test.go`
- 修改：`internal/agent/agent.go`

- [ ] **步骤 1：编写审批流程的失败测试**

添加五个独立测试：审批允许后执行；审批拒绝后不执行；无审批器时安全拒绝；审批器报错时安全拒绝并保留错误；事件顺序为 request 后 decision。回调必须收到：

```go
permission.Request{
    ToolCallID: "call-1",
    ToolName: "TestTool",
    Arguments: map[string]any{"path": "a.txt"},
}
```

- [ ] **步骤 2：运行 Ask 测试并确认失败**

运行：`go test ./internal/agent -run 'TestExecuteToolAsk'`

预期：FAIL，因为 Ask 分支尚未调用审批器和发送完整事件。

- [ ] **步骤 3：实现最少 Ask 审批逻辑**

```go
request := permission.Request{ToolCallID: call.ToolID, ToolName: call.ToolName, Arguments: call.Arguments}
sendAgentEvent(a.ctx, out, PermissionRequestEvent{ToolUseID: call.ToolID, ToolName: call.ToolName, Arguments: call.Arguments})
if a.permissionApprover == nil {
    err := errors.New("permission approver is not configured")
    sendAgentEvent(a.ctx, out, PermissionDecisionEvent{
        ToolUseID: call.ToolID, ToolName: call.ToolName, Granted: false, Err: err,
    })
    return tool.ToolResult{Output: err.Error(), IsError: true}
}
granted, err := a.permissionApprover(a.ctx, request)
sendAgentEvent(a.ctx, out, PermissionDecisionEvent{
    ToolUseID: call.ToolID, ToolName: call.ToolName,
    Granted: granted && err == nil, Err: err,
})
if err != nil || !granted {
    // 返回错误结果，不执行工具。
}
```

拒绝保持 agent loop 运行，并作为 `tool.ToolResult{IsError: true}` 返回。

- [ ] **步骤 4：运行 Agent 权限测试**

运行：`gofmt -w internal/agent/agent.go internal/agent/permission_test.go && go test ./internal/agent`

预期：PASS。

- [ ] **步骤 5：提交审批流程**

```bash
git add internal/agent/agent.go internal/agent/permission_test.go
git commit -m "feat: add interactive tool approval flow"
```

### 任务 4：配置默认应用并完成回归验证

**文件：**
- 修改：`internal/repl/ui.go`

- [ ] **步骤 1：定位默认 Agent 构造**

运行：`rg -n 'NewAgent|CreateDefaultTools' internal/repl/ui.go cmd`

预期：定位默认 Agent 和工具管理器初始化代码。

- [ ] **步骤 2：显式允许默认只读工具**

在 Agent 构造成功后设置：

```go
checker, err := permission.NewChecker(map[string]permission.Level{
    "ReadFile": permission.Allow,
})
if err != nil {
    return nil, fmt.Errorf("create permission checker: %w", err)
}
agent.SetPermissionChecker(checker)
```

未知工具不配置 Allow，继续默认 Ask。

- [ ] **步骤 3：运行仓库级验证**

运行：`gofmt -w internal/repl/ui.go && go vet ./... && go test ./...`

预期：所有命令退出码为 0。

- [ ] **步骤 4：检查最终差异**

运行：`git diff --check && git status --short`

预期：无空白错误；源代码和测试改动均属于本权限系统。

- [ ] **步骤 5：提交应用接线**

```bash
git add internal/repl/ui.go
git commit -m "feat: configure default read permission"
```

### 任务 5：最终验证与计划归档

**文件：**
- 验证：`internal/permission/permission.go`
- 验证：`internal/agent/agent.go`
- 验证：`internal/agent/events.go`
- 验证：`internal/repl/ui.go`
- 提交：`docs/superpowers/plans/2026-07-13-permission-system.md`

- [ ] **步骤 1：运行目标测试**

运行：`go test ./internal/permission ./internal/agent`

预期：PASS。

- [ ] **步骤 2：运行仓库级验证**

运行：`go vet ./... && go test ./...`

预期：所有包通过且无 vet 错误。

- [ ] **步骤 3：检查状态并提交计划**

```bash
git diff --check
git status --short
git add docs/superpowers/plans/2026-07-13-permission-system.md
git commit -m "docs: add permission system implementation plan"
```

预期：计划提交成功，工作区干净。
