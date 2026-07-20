# MCP 工具服务器

MyCode 支持通过标准输入输出（stdio）连接 [Model Context Protocol](https://modelcontextprotocol.io/) 服务器。启动时会读取工作区的 `.agent/mcp.yaml`；文件不存在时不会启动 MCP。

```yaml
mcpServers:
  filesystem:
    command: npx
    args:
      - -y
      - "@modelcontextprotocol/server-filesystem"
      - .
    env:
      EXAMPLE_OPTION: value
```

启动流程为 `initialize`、`notifications/initialized`、`tools/list`。发现到的工具会以 `mcp_<server>_<tool>` 注册，例如 `mcp_filesystem_read_file`，这样不会覆盖内置工具。调用会转换回原始 MCP 工具名并使用 `tools/call`。

MCP 工具仍会先通过本地权限管理器。默认策略为拒绝未知工具，因此应在 `.agent/permission.yaml` 中明确授权需要的 MCP 工具，例如：

```yaml
default: deny
workspace:
  root: .
tools:
  mcp_filesystem_read_file:
    permission: allow
    read_only: true
```

配置文件无效、服务器启动失败或初始化失败会使 CLI 初始化失败，避免静默地遗漏外部工具。
