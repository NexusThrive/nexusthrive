# services

Deployable backend processes.

- `api/`: the single Go binary. It wires the kernel, the platform layer and every registered distribution.
- `ai/`: Python service for analysis and AI agents. It reads from a replica and acts only through MCP. Added after the platform base.
