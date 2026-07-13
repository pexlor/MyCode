为什么需要权限管理？
如何找到安全和效率的平衡点？
需要防御什么？
多层防御如何设计？


# Agent 工具权限系统设计文档（Tool Permission System）

**作者：** 瑞瑞
**版本：** v1.0
**状态：** Draft

---

# 1. 背景

随着 Agent 具备 Tool Calling 能力，可以直接调用 Shell、文件操作、Git、网络请求等工具完成任务。

但 Tool 带来了新的安全风险，例如：

```bash
rm -rf /
rm -rf ~
find / -delete
dd if=/dev/zero of=/dev/sda
curl xxx | bash
git reset --hard
git clean -fd
```

如果 Agent 出现：

* Prompt Injection
* Jailbreak
* Tool 参数错误
* 模型幻觉（Hallucination）
* Bug

都有可能导致：

* 删除源码
* 覆盖用户文件
* 泄露密钥
* 修改系统配置
* 破坏开发环境

因此需要在 Tool Executor 与 Tool 实现之间增加统一的权限控制层。

---

# 2. 设计目标

## 2.1 功能目标

提供统一的 Tool 权限管理能力：

* Tool 权限控制
* 文件访问控制
* Shell 命令风险识别
* 用户确认
* 审计日志
* 可扩展策略

---

## 2.2 安全目标

遵循最小权限原则（Least Privilege）：

> 默认拒绝（Default Deny）

Agent 只能访问：

* Workspace
* 用户明确允许的资源

禁止访问：

* 系统目录
* 用户 Home
* SSH Key
* 系统配置

---

## 2.3 非目标

本设计不负责：

* LLM Prompt 安全
* 网络 ACL
* 容器安全

---

# 3. 总体架构

```
              +------------------+
              |       LLM        |
              +------------------+
                       │
                 Tool Call
                       │
                       ▼
            +----------------------+
            |   Tool Executor      |
            +----------------------+
                       │
                       ▼
          +--------------------------+
          | Permission Manager       |
          +--------------------------+
           │
           ├── Tool Policy
           ├── Path Validator
           ├── Command Analyzer
           ├── Risk Analyzer
           ├── User Confirmation
           └── Audit Logger
                       │
                 Permission
                       │
                       ▼
              Sandbox Executor
                       │
                       ▼
                 Tool Execute
```

整个权限系统位于：

```
Tool Executor
        │
        ▼
Permission Manager
        │
        ▼
真正执行 Tool
```

任何 Tool 都不能绕过 Permission Manager。

---

# 4. 核心设计

## 4.1 Permission Manager

统一权限入口。

```go
type PermissionManager interface {
    Authorize(
        ctx context.Context,
        req PermissionRequest,
    ) (PermissionResult, error)
}
```

所有 Tool 执行前必须调用：

```go
permission.Authorize(...)
```

---

## 4.2 Permission Request

统一描述一次 Tool 请求。

```go
type PermissionRequest struct {

    ToolName string

    Action string

    Arguments map[string]any

    Command string

    WorkingDirectory string

    ResolvedPaths []string

    RiskLevel RiskLevel

    RiskReasons []string
}
```

例如：

```
Tool:
    shell

Command:
    rm -rf build

WorkingDirectory:
    /home/user/project

ResolvedPaths:
    build/

Risk:
    High
```

---

## 4.3 Permission Result

```go
type PermissionDecision string

const (

    Allow PermissionDecision = "allow"

    Deny PermissionDecision = "deny"

    Confirm PermissionDecision = "confirm"
)
```

返回：

```go
type PermissionResult struct {

    Decision PermissionDecision

    Reason string
}
```

---

# 5. 风险等级

定义四级风险。

```go
type RiskLevel int

const (

    Safe RiskLevel = iota

    Low

    High

    Critical
)
```

对应策略：

| Risk     | 说明           | 默认行为    |
| -------- | ------------ | ------- |
| Safe     | 只读           | Allow   |
| Low      | 修改 Workspace | Allow   |
| High     | 删除、覆盖        | Confirm |
| Critical | 系统破坏         | Deny    |

---

## 示例

### Safe

```
pwd

ls

cat

grep

git status
```

自动执行。

---

### Low

```
echo >> file

touch

mkdir

go build
```

允许执行。

---

### High

```
rm build

git clean

git reset --hard
```

要求用户确认。

---

### Critical

```
rm -rf /

mkfs

dd

shutdown

reboot
```

永久拒绝。

---

# 6. Tool 权限

每个 Tool 声明自己的权限。

```go
type ToolPermission struct {

    ReadOnly bool

    CanWrite bool

    CanDelete bool

    AllowedPaths []string

    DeniedPaths []string

    RequireConfirm bool
}
```

例如：

## Read File

```
ReadOnly=true
```

## Write File

```
CanWrite=true
```

## Delete File

```
CanDelete=true

RequireConfirm=true
```

---

# 7. 文件权限控制

所有路径必须：

```
filepath.Clean()

↓

filepath.Abs()

↓

filepath.EvalSymlinks()

↓

是否位于 Workspace
```

禁止：

```
../../etc/passwd

/etc/passwd

~/.ssh

/proc

/sys

```

同时防止：

* Path Traversal
* Symbol Link Escape
* Absolute Path Escape

---

# 8. Shell 风险分析

Shell 工具增加 Command Analyzer。

流程：

```
Command

↓

Shell Parser

↓

AST

↓

Risk Analyzer

↓

Permission
```

分析：

* Program

```
rm
```

* 参数

```
-rf
```

* 重定向

```
>
```

* 管道

```
|
```

* Command Substitution

```
$()
```

* sudo

```
sudo
```

最终输出：

```
Risk = Critical

Reason = recursive delete root
```

---

# 9. 用户确认

High Risk 操作需要用户确认。

终端：

```
⚠ Agent 请求执行危险操作

Command:

rm -rf build

Impact:

删除 183 个文件

请选择：

[y] Allow Once

[s] Allow Session

[n] Deny
```

Critical 不允许确认。

直接返回：

```
Permission Denied

Reason:

system protected path
```

---

# 10. Policy 配置

增加：

```
.agent/

    permission.yaml
```

例如：

```yaml
default: deny

workspace:

  root: .

tools:

  shell:

    permission: confirm

  read_file:

    permission: allow

  write_file:

    permission: allow

protected_paths:

  - /

  - /etc

  - /boot

  - /usr

  - /var

  - ~/.ssh
```

支持团队统一配置。

---

# 11. 审计日志

记录所有 Tool 调用。

```
Time

Tool

Arguments

Decision

Risk

User

Duration
```

例如：

```json
{
  "tool":"shell",
  "command":"rm -rf build",
  "risk":"High",
  "decision":"Confirm",
  "user":"allow",
  "time":"2026-07-13T15:20:10"
}
```

方便排查问题。

---

# 12. 执行流程

```
LLM

↓

Tool Call

↓

Permission Request

↓

Policy Check

↓

Risk Analyze

↓

Need Confirm ?

↓

Yes ---------> User

↓

Permission Result

↓

Sandbox Execute

↓

Audit Log

↓

Return Tool Result
```

---

# 13. 模块划分

```
internal/

    permission/

        manager.go

        policy.go

        request.go

        result.go

        path.go

        analyzer.go

        risk.go

        confirm.go

        audit.go
```

职责：

| 文件          | 职责        |
| ----------- | --------- |
| manager.go  | 权限入口      |
| policy.go   | Policy 加载 |
| analyzer.go | Shell 分析  |
| risk.go     | 风险计算      |
| path.go     | 路径校验      |
| confirm.go  | 用户确认      |
| audit.go    | 日志        |

---

# 14. 后续规划

## v1

* Tool 白名单
* Workspace 限制
* 路径校验
* Risk 分级
* 用户确认

---

## v2

* Shell AST 分析
* Tool Capability
* Session Permission
* Policy 热更新

---

## v3

* Docker Sandbox
* Linux Namespace
* seccomp
* AppArmor
* SELinux
* Fine-grained Capability

---

# 15. 总结

本设计采用统一的 **Permission Manager** 作为所有 Tool 的唯一权限入口，通过 **权限策略（Policy）+ 风险分析（Risk Analysis）+ 路径校验（Path Validation）+ 用户确认（Confirmation）+ 审计日志（Audit）** 五个模块，实现 Agent 工具调用的安全控制。

系统遵循 **默认拒绝（Default Deny）** 和 **最小权限（Least Privilege）** 原则，高风险操作需要用户确认，涉及系统目录或破坏性命令的操作则直接拒绝执行。同时，通过可配置的策略文件和统一接口，为后续接入 Docker Sandbox、seccomp 等更强的隔离机制预留了扩展能力，确保权限系统既能满足开发效率，也具备生产环境所需的安全性。


