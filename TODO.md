# 系统优化
1. 工具调用被权限系统拦截也要把信息返回给模型 done
2. 工具注册有问题，模型对话表示没有一些工具（工具被拦截了） done
3. 支持用户选择权限级别（）
4. 支持显示对话状态（思考？工具调用？）done
5. ctrl + c 打断对话 / 非模型输出状态退出命令行 done
6. ui 变好看一点（开头 + 对话框优化）done
7. 命令行输入的时候，输入法位置有问题 done
8. 支持多系统架构（长期）
9. / 选择带参数的命令时不要直接执行 比如（/new xxx）done
10. 持久化session优化
11. 完善发布入口（--version --help）done
12. 输出完整的工具执行命令(取消)
13. 优化系统输入
14. ui 适配 markdown done
15. 开启think功能/内置命令支持开关 done
16. 按上下键，可以跳出历史输入 done

# 模块功能实现
1. 长期记忆
2. subagent
3. agent team
4. skill 系统
5. hook 系统

# 长期任务
1. 架构优化（人类架构）
2. 权限系统优化（优化拦截策略）
3. 上下文优化（最大化利用模型缓存）
4. 完善模块设计文档
5. 完善测试系统
6. 支持多系统架构
7. ui 优化
8. 日志系统

# bug
1. 权限bug 已修复
● 执行失败: active path is outside workspace
  输入已取消
› 你好

● 执行失败: active path is outside workspace
