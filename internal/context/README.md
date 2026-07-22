# 上下文与会话存储

`internal/context` 在每次模型请求前构建受 Token 预算约束的 `ContextView`，同时把完整
会话事实保存在工作区的 `.context/sessions/<session-id>` 中。摘要、工具结果卸载和淘汰
只改变模型视图，不删除 `transcript.jsonl` 中的原始消息。

每个会话目录包含：

- `manifest.json`：格式版本、标题、工作区、消息数、更新时间和摘要检查点；
- `transcript.jsonl`：按顺序追加的用户、assistant、工具调用和工具结果；
- `summaries/`：已经提交的增量摘要；
- `tool-results/`：大工具结果的正文、元数据和 SHA256。

REPL 每次启动分配新的逻辑会话，第一条用户消息出现时才创建目录。可用命令：

- `/new [标题]`：创建新会话；
- `/sessions`：列出当前工作区最近的会话；
- `/resume <id>`：用完整 ID 或唯一前缀恢复会话；
- `/rename <标题>`：重命名当前会话；
- `/current`：显示当前会话状态；
- `/delete <id>`：确认后删除非当前会话。

`FileConversationStore` 是 transcript 的权威来源。恢复会话时，`MessageManager` 从完整
transcript 重建；`ContextManager` 会识别已有前缀，只追加新消息，避免重复写入。
