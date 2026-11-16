# contracts

The single source of truth for every interface between components.

```
contracts/
├── proto/          operations (Protobuf + Connect-RPC)
│   ├── kernel/v1/
│   ├── platform/v1/
│   └── business/v1/   (stage 2)
├── events/         event payloads (JSON Schema, versioned: *.v1.json)
├── buf.yaml
└── buf.gen.yaml    generates Go (kernel/gen) and TypeScript (packages/sdk)
```

Every operation declares its required permission, capability, risk (`read`, `write`, `money`) and idempotency. Breaking changes are detected by `buf breaking`.

Documentation (Spanish): `docs/02-arquitectura/contrato.md`.

The initial `kernel.v1.KernelService` exposes `WriteExchange` and `ReadEvents`.
Method options carry the operation policy used by the engine and future MCP
generation. `kernel.exchange.recorded.v1.json` describes the event payload.

Run `pnpm install --frozen-lockfile` and `task tools:contracts` once to prepare
the pinned local plugins. After preparation, `task gen` runs without network
access. Generated Go and TypeScript files are committed and never edited by hand.
`task contracts:check` compares against `CONTRACT_BASE_REF` (default: `main`);
the first-contract baseline is reported explicitly when no prior Protobuf exists.
