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
