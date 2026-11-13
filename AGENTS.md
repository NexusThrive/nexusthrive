# AGENTS.md

Guide for coding agents working on NexusThrive. Read it entirely before changing anything. Project documentation lives in `docs/` and is written in Spanish; this guide summarizes it in English.

## What NexusThrive is

NexusThrive is a **kernel** that structures ecosystems and keeps them running. It knows nothing about businesses: it only knows seven primitives (`Participant`, `Resource`, `Exchange`, `Commitment`, `Event`, `Timer`, `Grant`/`Capability`) and enforces five laws (conservation, memory, commitment, permission, motion).

Three layers sit around it:

- **Platform** (`platform/`, `apps/console`): above everything. NexusThrive staff create, bill, configure, monitor and support every space.
- **Distributions** (`distributions/`): worlds on top of the kernel. The first one is **Thrive Business**, with patterns (Membership, Products, Preparation, Service, Project) and configuration skins (gym, store, restaurant).
- **Apps** (`apps/`): user-facing clients with no business logic.

## Current priority

**Stage 1: the NexusThrive base.** Build the kernel, the gateways, the platform layer and the console before any Business feature. Business starts only after phase B5. See `docs/02-arquitectura/hoja-de-ruta.md`.

| Phase | Scope |
|---|---|
| B1 | Scaffolding: workspaces, Taskfile, local infrastructure, CI |
| B2 | Kernel core: spaces, identities, participants, grants, ledger, commitments, timers, events, outbox, idempotency |
| B3 | Gateways: Connect API, read-only MCP, signed webhooks, Clerk token validation |
| B4 | Platform layer: staff, plans, subscriptions, charges, benefit codes, skin releases, capability requests, support access |
| B5 | NexusThrive console (`apps/console`) |
| B6 | Hardening: isolation tests, staff MFA, observability, backup and restore |

## Read in this order

1. `docs/00-identidad/manifiesto.md`, `docs/00-identidad/glosario.md`
2. `docs/01-modelo/primitivas.md`, `docs/01-modelo/leyes.md`
3. `docs/02-arquitectura/` — start with `vision-general.md`, `estructura.md`, `kernel.md`, `hoja-de-ruta.md`
4. `docs/02-arquitectura/modelo-de-datos/` — `kernel.md` and `plataforma.md` first
5. `docs/02-arquitectura/plataforma/` — console, flows and security
6. `docs/05-decisiones/` — every ADR
7. `docs/06-reglas/` and `docs/07-ingenieria/`
8. `docs/03-distribuciones/business/` — only when stage 2 starts

If something is not documented, ask before inventing it.

## Repository layout

| Path | Contents | Language |
|---|---|---|
| `kernel/` | Core packages: `gateway`, `capability`, `execution`, `extension`, `model`, `state`, `coordination`, `laws`, `db` | Go |
| `platform/` | Privileged extension for NexusThrive staff; owns the `platform` schema | Go |
| `distributions/business/` | `translator`, `base`, `capabilities`, `patterns`, `skins`, `adapters`, `ai`, `db` | Go + YAML |
| `services/api/` | The single binary that wires kernel, platform and distributions | Go |
| `services/ai/` | Analysis and AI agents; read replica, actions through MCP | Python |
| `apps/console`, `apps/web`, `apps/mobile`, `apps/desktop` | User-facing apps | TypeScript + React |
| `packages/brand`, `ui`, `app`, `sdk`, `i18n`, `config` | Shared TypeScript packages | TypeScript |
| `contracts/` | Protobuf operations and JSON Schema events; single source of truth | Protobuf, JSON Schema |
| `integrations/n8n/` | n8n node and versioned workflows | n8n |
| `infra/`, `tools/` | Local infrastructure and repository tooling | YAML, Node |
| `docs/` | Documentation | Spanish |

## Non-negotiable rules

1. **The kernel learns no business words.** No `customer`, `invoice`, `sale`, `membership` in `kernel/`. An architecture test enforces it.
2. **No cross-imports.** `kernel/` never imports `platform/` or `distributions/`; they register through `kernel/extension`. `platform/` and distributions never import each other. Apps talk to the backend only through `@nexusthrive/sdk`.
3. **Only the kernel and its extensions touch the database.** Each layer writes only to its own schema (`kernel`, `platform`, `business`).
4. **One operation, one transaction.** Ledger entries, commitments, events and outbox rows are written together or not at all.
5. **Conservation:** entries of an exchange sum to zero per resource. **Memory:** events and entries are append-only; corrections are compensating exchanges.
6. **Every operation declares** permission, capability, risk (`read`, `write`, `money`) and idempotency in `contracts/`. Money operations are always idempotent.
7. **Logic and view never mix.** Apps render and call the SDK.
8. **Critical integrations are direct; the rest goes through n8n.** Payments and e-invoicing are distribution adapters. n8n never stores or moves value.
9. **Business owners never toggle capabilities.** Capabilities come from the skin package, the AI (within suggested ones) or NexusThrive staff.
10. **Privileged access is temporary and audited.** Staff access to a customer space requires a reason, expires, and is written to `kernel.access_audit`.
11. **Build only what the current phase requires.** Unfinished work is never exposed.
12. **Decisions first.** A significant change requires a new ADR in `docs/05-decisiones/` before the code.

## Language policy

| Item | Language |
|---|---|
| `docs/`, root `README.md` | Spanish |
| Code, identifiers, comments, logs, internal errors, tests | English |
| File and folder names in code, tables, columns, contracts | English |
| Commits, pull requests, issues, branch names | English |
| User-facing text | Internationalized via `@nexusthrive/i18n` (`es` default, `en`) |

Product names (NexusThrive, Thrive Business) are never translated. Full policy: `docs/07-ingenieria/language-policy.md`.

## Code conventions

- Comments explain purpose, constraints and reasons, placed right above the element. Go doc comments start with the element name. No narration of obvious code.
- Go 1.24, `go.work`, `sqlc` + `pgx`, Connect-RPC, typed contract errors, `fmt.Errorf("context: %w", err)`.
- TypeScript strict mode, pnpm workspaces, Turborepo, React + Vite, Tailwind; every component starts in Storybook and covers loading, empty, data and error states. Components use only the tokens from `packages/brand`; never raw colors, shadows, z-index values or durations. Design rules: `docs/06-reglas/sistema-de-diseno`.
- Money uses exact decimals (`numeric` in PostgreSQL, a decimal type in Go). Never `float`.
- IDs are UUID v7. Timestamps are `timestamptz` in UTC. Default locale `es-CO`, timezone `America/Bogota`, currency `COP`.

## Git

- Branches must match `^(feature|fix|chore|hotfix)/HU-[a-z0-9]+(-[a-z0-9]+)*-[0-9]{2}$`, in English, e.g. `feature/HU-kernel-ledger-01`. Sprint integration branches are `sprint/S<nn>`. `feat/`, `docs/` or any other prefix is rejected (`feat/HU-kernel-ledger-01` is invalid). The branch prefix is not the commit type: a `feature/` branch has `feat`, `docs` or `test` commits. Never commit to `main`.
- Commits: Conventional Commits in English, e.g. `feat(kernel): enforce ledger conservation per resource`.
- Commit author: **santy401** (`santy401@users.noreply.github.com`).
- **Never add `Co-Authored-By` trailers, signatures, links or mentions of tools or assistants** in commits, pull requests or files.
- One pull request per story, using `.github/pull_request_template.md`, with `docs/` updated in the same change.

## Testing

- Write the test before the implementation for every operation.
- `kernel/laws/` holds tests for the five laws; they are never skipped.
- Isolation tests: no data crosses spaces. Idempotency tests for every money operation.
- Run `task lint`, `task test` and `task docs:check` before opening a pull request.

## Commands

| Command | Purpose |
|---|---|
| `task setup` | Install dependencies |
| `task infra:up` | Start PostgreSQL and Redis |
| `task gen` | Generate code from contracts |
| `task test` | Run all tests |
| `task lint` | Lint Go, TypeScript and contracts |
| `task docs:check` | Render every Mermaid diagram in `docs/` |
