# ACP Agent

`acp` adapts one remote stable-v1 Agent Client Protocol peer into a gopact `agent.Agent`.

The caller supplies an already-open bidirectional transport. `New` performs ACP initialization; each `Invoke` creates one remote ACP session, maps text and artifact references from the complete gopact request into one prompt, and collects agent-message updates into the final response. Context cancellation also cancels the active ACP request.

The adapter deliberately does not provide permission approval, filesystem, terminal, authentication, or persistent remote-session semantics. Permission requests are declined. Add those capabilities when the application has an explicit policy and client implementation.
