# 用户级配置文件设计

## 背景与目标

MyCode 当前通过 `MYCODE_*` 和 `ANTHROPIC_*` 环境变量读取模型、摘要模型和上下文预算配置。日常使用时需要反复导出多项环境变量，不便于维护和切换。

本次改动引入用户级 YAML 配置文件 `~/.mycode/config.yaml`，将其作为主要配置来源，同时保留现有环境变量作为可选覆盖层，以兼容已有脚本、CI 和临时调试方式。

## 配置格式

配置文件采用以下结构：

```yaml
model:
  protocol: anthropic
  base_url: https://api.anthropic.com
  api_key: sk-ant-...
  name: claude-sonnet-4-5
  max_tokens: 4096

summary:
  model: ""
  base_url: ""
  api_key: ""

context:
  window: 200000
  output_reserve: 8192
```

字段含义如下：

- `model.protocol`：模型接口协议，当前支持 `openai-compat` 和 `anthropic`。
- `model.base_url`：模型服务基础地址。
- `model.api_key`：模型服务凭据。
- `model.name`：主模型名称。
- `model.max_tokens`：单次模型输出上限；未配置时沿用客户端安全默认值。
- `summary.model`：摘要模型名称；为空时复用主模型客户端。
- `summary.base_url`：摘要模型服务地址；摘要模型启用但此项为空时复用主模型地址。
- `summary.api_key`：摘要模型凭据；配置独立摘要模型时必填。
- `context.window`：上下文窗口大小。
- `context.output_reserve`：为模型输出预留的 token 数。

## 查找与加载

程序通过 `os.UserHomeDir()` 获取用户主目录，并固定读取 `<home>/.mycode/config.yaml`。首个版本不支持命令行指定其他路径，也不引入多 profile，避免扩大配置系统范围。

启动加载顺序为：

1. 读取并解析用户级 YAML 文件。
2. 应用字段默认值。
3. 使用已存在的环境变量覆盖对应字段。
4. 校验合并后的最终配置。
5. 使用最终配置创建主模型、摘要模型和上下文管理组件。

配置文件不存在时，启动失败，并在错误中给出完整路径和最小配置示例提示。YAML 无法解析时，错误包含配置文件路径和底层解析原因。

## 环境变量兼容

以下现有变量继续支持，并优先于配置文件：

| 环境变量 | YAML 字段 |
| --- | --- |
| `MYCODE_PROTOCOL` | `model.protocol` |
| `ANTHROPIC_BASE_URL` / `MYCODE_BASE_URL` | `model.base_url` |
| `ANTHROPIC_API_KEY` / `MYCODE_API_KEY` | `model.api_key` |
| `MYCODE_MODEL` | `model.name` |
| `MYCODE_MAX_TOKENS` | `model.max_tokens` |
| `MYCODE_SUMMARY_MODEL` | `summary.model` |
| `MYCODE_SUMMARY_BASE_URL` | `summary.base_url` |
| `MYCODE_SUMMARY_API_KEY` | `summary.api_key` |
| `MYCODE_CONTEXT_WINDOW` | `context.window` |
| `MYCODE_OUTPUT_RESERVE` | `context.output_reserve` |

同一字段存在两种历史环境变量时，协议专用变量优先于 `MYCODE_*` 变量。`MYCODE_BASH` 继续只通过环境变量读取，因为它描述当前运行环境，而不是模型配置。

## 代码边界

`internal/config` 负责配置结构、默认值、文件读取、环境变量覆盖和最终校验。该包输出一个已经可供运行时直接使用的配置对象，不负责创建 LLM 客户端。

`internal/repl` 只消费最终配置并组装运行时，不再自行散落读取模型和上下文相关环境变量。欢迎页使用实际加载后的模型名称，而不是再次读取环境变量。

这一边界让配置加载逻辑可以脱离交互式 REPL 单独测试，也避免不同运行时组件采用不同的配置优先级。

## 校验与错误处理

最终配置至少校验：

- `model.protocol`、`model.base_url`、`model.api_key` 和 `model.name` 非空。
- 数值字段为正数，并满足上下文预算现有约束。
- 配置独立摘要模型时，摘要 API Key 非空；摘要地址为空时允许复用主模型地址。
- 不支持的协议由现有 LLM 客户端工厂返回明确错误。

错误信息包含 YAML 字段名；读取或解析错误同时包含配置文件路径。环境变量中的非法整数不得静默覆盖有效 YAML 值，而应报告具体变量名及非法值。

配置文件包含凭据。程序发现 Unix 文件权限允许 group 或 others 读取时，在标准错误输出警告并建议执行 `chmod 600 ~/.mycode/config.yaml`，但不阻止启动。程序不记录或回显 API Key。

## 测试策略

配置包增加表驱动测试，覆盖：

- 完整 YAML 成功加载。
- 缺失文件和非法 YAML 的错误信息。
- 默认值应用。
- 环境变量覆盖 YAML。
- 两种历史环境变量的优先级。
- 必填字段缺失和非法整数。
- 摘要模型复用主模型地址。
- 权限过宽时产生警告且仍能加载。

REPL 相关测试验证运行时使用加载后的模型名，并确认欢迎页展示最终配置值。完成后运行 `go test ./...` 和 `go vet ./...`。

## 非目标

本次不实现多 profile、项目级配置、配置热加载、凭据钥匙串、自动创建配置文件或命令行配置编辑器。后续如有明确需求，可在当前加载器接口上扩展。
