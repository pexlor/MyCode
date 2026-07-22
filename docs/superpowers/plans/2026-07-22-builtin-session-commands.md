# 内置会话命令实现计划

> **面向 AI 代理的工作者：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans 逐任务实现此计划。步骤使用复选框（`- [ ]`）语法来跟踪进度。

**目标：** 为 MyCode REPL 增加可持久化的新建、列出、恢复、删除、重命名和查看当前会话命令。

**架构：** 在 `internal/context` 的文件 Store 上增加会话 metadata 生命周期，在独立的 `internal/session` 服务中维护当前会话，再由 `internal/repl` 的命令注册表进行解析和展示。Agent 增加可切换 Session ID 的安全入口，恢复时从 transcript 重建 `MessageManager`。

**技术栈：** Go 1.24、标准库、现有 `internal/context`、表驱动测试、`go test`/`go vet`。

---

## 文件结构

- 修改 `internal/context/types.go`：定义 `SessionMetadata`，扩展 manifest 字段。
- 修改 `internal/context/store.go`：定义会话目录生命周期接口和会话错误。
- 修改 `internal/context/file_store.go`：实现创建、查询、列表、改名和安全删除。
- 修改 `internal/context/file_store_test.go`：覆盖 metadata、工作区过滤、前缀解析所需列表和删除安全。
- 创建 `internal/session/service.go`：维护当前会话、延迟落盘、恢复和标题规则。
- 创建 `internal/session/service_test.go`：用内存 Store 验证状态切换和失败原子性。
- 创建 `internal/repl/command.go`：实现命令注册、解析、帮助和 handler。
- 创建 `internal/repl/command_test.go`：覆盖解析、帮助及所有会话命令。
- 修改 `internal/repl/ui.go`：注入 IO、初始化 Service/Registry，并把普通消息和 Agent 连接起来。
- 创建 `internal/repl/ui_test.go`：验证 EOF、清屏、命令不进入历史和恢复后的对话。
- 修改 `internal/agent/agent.go`：允许 REPL 在串行空闲状态切换 Session ID。

### 任务 1：扩展文件会话存储

**文件：**
- 修改：`internal/context/types.go`
- 修改：`internal/context/store.go`
- 修改：`internal/context/file_store.go`
- 测试：`internal/context/file_store_test.go`

- [ ] **步骤 1：编写失败的生命周期测试**

测试创建带标题和工作区的会话，追加消息后计数增加，列表按更新时间倒序并过滤工作区，改名写入 manifest，删除后目录不存在；同时验证非法 ID 和符号链接目标被拒绝。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/context -run 'TestFileStore(SessionLifecycle|ListSessions|DeleteSession)' -v`

预期：FAIL，缺少 `SessionMetadata`、`CreateSession`、`GetSession`、`ListSessions`、`RenameSession` 或 `DeleteSession`。

- [ ] **步骤 3：实现最小 Store API**

增加：

```go
type SessionMetadata struct {
	ID            string    `json:"session_id"`
	Title         string    `json:"title"`
	Workspace     string    `json:"workspace"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	MessageCount  int       `json:"message_count"`
	FormatVersion int       `json:"format_version"`
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

`AppendMessage` 在成功 `fsync` 后原子更新 manifest 的时间和计数。删除前使用 `Lstat` 拒绝符号链接，并验证 `filepath.Rel(root, target)` 不以 `..` 开头。

- [ ] **步骤 4：运行 Store 测试**

运行：`go test ./internal/context -v`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/context/types.go internal/context/store.go internal/context/file_store.go internal/context/file_store_test.go
git commit -m "feat: add session lifecycle storage"
```

### 任务 2：实现 SessionService

**文件：**
- 创建：`internal/session/service.go`
- 创建：`internal/session/service_test.go`

- [ ] **步骤 1：编写失败的服务测试**

构造内存 `contextmanager.SessionStore`，验证启动分配未持久化会话、第一条消息延迟创建、默认标题、显式标题、New、Resume 的先加载后切换、Rename 回滚、禁止删除当前会话、ID 唯一前缀和 Close 空会话不落盘。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/session -v`

预期：FAIL，包和 `Service` 尚不存在。

- [ ] **步骤 3：实现服务和转换函数**

核心 API：

```go
func NewService(store contextmanager.SessionStore, workspace, systemPrompt string) (*Service, error)
func (s *Service) New(ctx context.Context, title string) (*CurrentSession, error)
func (s *Service) List(ctx context.Context, limit int) ([]SessionSummary, error)
func (s *Service) Resume(ctx context.Context, idOrPrefix string) (*CurrentSession, error)
func (s *Service) Delete(ctx context.Context, idOrPrefix string) error
func (s *Service) Rename(ctx context.Context, title string) error
func (s *Service) AddUserMessage(ctx context.Context, content string) error
func (s *Service) Current() CurrentSession
```

恢复时将 `StoredToolUse`/`StoredToolResult` 无损转换为消息块；新 ID 使用 `crypto/rand` 生成，不依赖时间戳碰撞概率。

- [ ] **步骤 4：运行服务测试**

运行：`go test ./internal/session -v`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/session
git commit -m "feat: add session lifecycle service"
```

### 任务 3：实现命令注册表和会话命令

**文件：**
- 创建：`internal/repl/command.go`
- 创建：`internal/repl/command_test.go`

- [ ] **步骤 1：编写失败的解析与 handler 测试**

覆盖普通文本、未知命令、大小写、参数保留、重复注册、自动帮助，以及 `/new`、`/sessions`、`/resume`、`/delete`、`/rename`、`/current`、`/clear`、`/exit` 的输出和错误路径。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/repl -run 'TestCommand' -v`

预期：FAIL，命令注册类型不存在。

- [ ] **步骤 3：实现注册表**

解析仅切分第一个空白；未知斜杠命令返回 handled error。`/help` 遍历注册顺序。删除 handler 通过注入的 `*bufio.Reader` 和 `io.Writer` 读取 `[y/N]`，只有 `y/yes` 执行。

- [ ] **步骤 4：运行命令测试**

运行：`go test ./internal/repl -run 'TestCommand' -v`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/repl/command.go internal/repl/command_test.go
git commit -m "feat: add builtin session commands"
```

### 任务 4：接入 REPL 与 Agent 会话切换

**文件：**
- 修改：`internal/agent/agent.go`
- 修改：`internal/repl/ui.go`
- 创建：`internal/repl/ui_test.go`

- [ ] **步骤 1：编写失败的 REPL 生命周期测试**

注入字符串输入、缓冲输出、临时 Store 和假 runner，验证启动不恢复旧会话、命令不进入历史、`/clear` 不切换会话、`/resume` 后使用恢复历史、EOF 正常关闭且不输出读取错误。

- [ ] **步骤 2：运行测试验证失败**

运行：`go test ./internal/repl ./internal/agent -v`

预期：FAIL，REPL 仍绑定进程级 IO 和固定 Session ID。

- [ ] **步骤 3：重构初始化和输入循环**

让初始化返回共享 Store、ContextManager 和 Agent；Service 切换成功后调用
`runner.SetContextManager(contextManager, current.ID)`。普通输入先通过 Service 加入当前
`MessageManager`，Agent 完成后由现有 ContextManager 同步完整历史。EOF 走正常退出路径。

- [ ] **步骤 4：运行相关测试**

运行：`go test ./internal/repl ./internal/agent -v`

预期：PASS。

- [ ] **步骤 5：提交**

```bash
git add internal/agent/agent.go internal/repl/ui.go internal/repl/ui_test.go
git commit -m "feat: integrate persistent sessions into repl"
```

### 任务 5：全量验证和文档同步

**文件：**
- 修改：`internal/context/README.md`
- 修改：`TODO.md`

- [ ] **步骤 1：更新用户可见命令和存储说明**

在 Context README 记录 manifest 新字段和会话生命周期命令；从 TODO 中移除已经完成的
会话管理条目（若不存在对应条目则不改 TODO）。

- [ ] **步骤 2：格式化并检查差异**

运行：`gofmt -w internal/context internal/session internal/repl internal/agent`

运行：`git diff --check`

预期：均无错误。

- [ ] **步骤 3：运行完整测试与静态检查**

运行：`go test ./...`

运行：`go vet ./...`

预期：全部通过。

- [ ] **步骤 4：提交收尾**

```bash
git add internal/context/README.md TODO.md
git commit -m "docs: document builtin session commands"
```
