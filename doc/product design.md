# 产品定位
myCode 是一个面向开发者的 CLI 代码智能代理（Code Agent）系统，具备代码理解、修改、执行与自动化修复能力，通过自然语言驱动本地代码仓库操作。

核心定位：
· “终端里的 AI 工程助手”
· 具备工具执行能力的 agent，而不仅是 chat
· 能理解 repo、修改代码、跑测试、修 bug

# 产品形态
## CLI
仅支持 CLI
'''
mycode chat
mycode run "fix login bug"
mycode apply "refactor auth module"
mycode test
mycode commit "fix auth bug"
mycode index
''' 

## 运行模式
1. chat 模式（交互式）
持续对话 + 工具调用
2. task 模式 （单次任务） 

3. repo 模式 （代码仓库 agent）
绑定当前 git repo：
    自动读取结构
    自动索引文件
    可执行 patch / commit / test

# 核心能力
## 多轮对话能力
支持上下文连续对话，并具备任务状态延续能力
特点
    session 级上下文
    支持中断恢复
    支持任务继续执行
## 短期记忆
存储内容（Redis）
    最近对话 N 轮
    tool execution history
    当前 task state
    生命周期
    session 结束自动过期（默认 24h）
## 长期记忆
存储（MySQL）
    用户级记忆
    编码习惯
    常用技术栈
    偏好（比如不喜欢重构）
项目级记忆
    repo 结构摘要
    常见 bug 模式
    架构说明
## 代码理解能力
功能
    自动扫描 repo
    构建 symbol 索引
    支持快速定位代码
方法
    AST parsing（Go/TS/Python）
    grep fallback
    embedding search（增强）
## Tool Execution Syste
工具调用
    内置基础工具集
## 任务规划
