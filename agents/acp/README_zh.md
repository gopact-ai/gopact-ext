# ACP Agent

`acp` 把一个远端 stable-v1 Agent Client Protocol peer 适配为 gopact `agent.Agent`。

调用方提供已经建立的双向 transport。`New` 完成 ACP 初始化；每次 `Invoke` 创建一个远端 ACP session，把完整 gopact request 中的 text 和 artifact reference 映射为一次 prompt，并把 agent-message update 收敛为最终 response。Context 取消时也会取消当前 ACP request。

该 adapter 不提供 permission approval、文件系统、终端、认证或持久远端 session 语义；permission request 默认拒绝。应用具备明确策略和 client 实现后再增加这些可选能力。
