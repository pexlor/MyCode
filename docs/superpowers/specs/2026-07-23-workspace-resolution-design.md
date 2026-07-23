# Workspace 解析与传递设计

## 背景

FFCode 当前在多个组件中分别调用 `os.Getwd()`，包括 REPL、系统提示词、工具权限和 MCP 配置加载。这会产生两个问题：

1. 从 `cmd/FFCode` 启动二进制时，该子目录会被误认为整个 workspace；
2. 即使某一层改用了其他 workspace，其他组件仍可能继续使用进程工作目录，形成不一致的权限和上下文边界。

此外，`activePaths` 会按参数名包含 `path`、`file` 或 `directory` 来识别活跃文件路径，因此通用的 `working_directory` 也会被错误地用于加载路径级规则。

## 目标

- 支持 `--cwd <path>` 显式指定 workspace；
- 未指定 `--cwd` 时，优先使用当前目录所属 Git 仓库的根目录；
- Git 不可用或当前目录不属于 Git 仓库时，回退到当前目录；
- 将最终 workspace 显式传递给所有依赖组件，不修改进程工作目录；
- `activePaths` 不再把 `working_directory` 当成活跃文件路径；
- 保持现有无参数工具构造入口兼容。

## 非目标

- 不支持一次会话配置多个 workspace；
- 不新增项目配置文件中的 workspace 字段；
- 不改变权限策略文件中 `workspace.root` 的覆盖语义；
- 不重新设计工具参数的完整路径元数据协议；
- 不调用 `os.Chdir` 改变进程级工作目录。

## Workspace 解析规则

解析只在启动阶段执行一次，结果为经过清理和符号链接解析的绝对目录。

### 显式 `--cwd`

当用户提供 `--cwd` 时：

1. 相对路径以进程启动目录为基准；
2. 路径必须存在且必须是目录；
3. 解析为规范绝对路径；
4. 直接将该目录作为 workspace，不再向上提升到 Git 根目录；
5. 路径无效时返回启动错误，不回退到其他目录。

### 默认解析

当用户未提供 `--cwd` 时：

1. 取得进程当前目录并规范化；
2. 通过 `git -C <当前目录> rev-parse --show-toplevel` 探测 Git 根目录；
3. Git 命令成功且返回有效目录时，使用该根目录；
4. Git 不存在、命令失败、当前目录不是仓库或结果无效时，使用规范化后的当前目录。

Git 探测通过 `os/exec` 直接执行，不经过 Shell，也不改变进程目录。

## 组件设计

### Workspace Resolver

新增独立的 workspace 解析组件，职责仅包括：

- 校验并规范化显式目录；
- 探测默认 Git 根目录；
- 在默认探测失败时安全回退。

解析器提供可替换的 Git 根目录查找函数，使单元测试不依赖测试机器是否安装 Git。

### CLI 参数

REPL 启动入口使用标准库 `flag.FlagSet` 解析 `--cwd`。`cmd/FFCode/main.go` 将 `os.Args[1:]` 传给 REPL，不在 `main` 中复制 workspace 逻辑。

未知参数、缺失的 `--cwd` 值和无效目录都作为启动错误显示。`--help` 使用标准 flag 帮助输出。

### 显式依赖传递

解析出的 workspace 由 REPL 启动流程向下传递给：

- 欢迎界面的 directory 字段；
- 系统提示中的 Current working directory；
- Session Service 和 `.context/sessions` 存储路径；
- ContextManager 的规则加载边界；
- 默认工具权限策略和 `.agent/permission.yaml`；
- `.agent/mcp.yaml` 配置加载。

工具层新增接收 workspace 的构造入口。现有无参数构造函数继续使用当前目录，并作为兼容包装保留，避免破坏已有测试和内部调用者。

## 活跃路径规则

`activePaths` 继续只读取执行成功的工具调用。对每个成功调用：

1. 参数名规范化为小写，并忽略 `_` 和 `-`；
2. 规范名为 `workingdirectory` 时跳过；
3. 其他包含 `path`、`file` 或 `directory` 的参数继续作为候选路径；
4. `cwd` 当前不会命中路径关键词，行为保持不变。

这保证 `file_path` 等真实目标仍能触发局部规则，同时通用执行目录不会成为虚假的活跃文件。

## 错误处理

- 显式 `--cwd` 无效：启动失败并显示具体路径错误；
- 默认 Git 探测失败：静默回退当前目录；
- 无法读取进程当前目录：启动失败；
- workspace 传给某个组件后初始化失败：沿用现有初始化错误处理，不进行第二次 workspace 推断；
- 外部活跃路径：由 `LoadRules` 跳过，不中断 Context Build。

## 测试设计

### 活跃路径

- 成功工具调用同时包含 `file_path` 和 `working_directory` 时，只返回 `file_path`；
- `working-directory` 和 `workingDirectory` 使用相同的忽略规则；
- 失败和 pending 工具调用仍不产生路径。

### Workspace Resolver

- 显式绝对目录严格作为 workspace，不调用 Git 探测；
- 显式相对目录相对启动目录解析；
- 显式目录不存在或指向文件时返回错误；
- 未显式指定时使用 Git 根目录；
- Git 探测失败时回退当前目录；
- 返回路径经过绝对化和符号链接规范化。

### CLI 与组件传递

- `--cwd` 参数被正确解析；
- 未知参数和缺失值返回错误；
- 系统提示使用解析后的 workspace；
- 工具权限和 MCP 配置从解析后的 workspace 初始化；
- Session 和 ContextManager 使用同一个 workspace。

## 兼容性与迁移

- 不传 `--cwd` 的用户在 Git 仓库内启动时，workspace 会从当前子目录提升为仓库根目录；这是本次有意的行为变化；
- 非 Git 目录保持使用当前目录；
- 现有 `CreateDefaultTools` 和 `CreateDefaultToolsWithMCP` 调用继续可用；
- 已持久化的 Session 仍按 manifest 中的 workspace 过滤，不自动迁移旧 workspace 会话；用户可从旧目录显式启动或新建会话。

## 验收标准

1. 从仓库子目录启动 FFCode 时，默认 workspace 为 Git 根目录；
2. `--cwd` 指向仓库子目录时，workspace 精确等于该子目录；
3. 所有初始化组件使用同一个解析结果；
4. 通用 `working_directory` 不再进入 `activePaths`；
5. 外部路径和被拒绝工具调用不会使后续 Context Build 失败；
6. 全量 Go 测试通过。
