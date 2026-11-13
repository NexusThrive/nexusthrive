# Stack

## Propósito

Listar la tecnología oficial de cada capa.

## Alcance

Lenguajes, librerías principales y herramientas del monorepo. Cambiar algo de esta lista exige un ADR.

## Conceptos principales

> **Go decide, PostgreSQL recuerda, Python analiza, n8n conecta y React muestra.**

| Capa | Tecnología |
|---|---|
| Kernel, plataforma y distribuciones | Go 1.24, Connect-RPC, `sqlc`, `pgx`, decimales exactos |
| Datos | PostgreSQL 17 (esquemas `kernel`, `platform`, `business`), Redis 7 |
| Señales | Outbox en PostgreSQL; NATS JetStream cuando el volumen lo pida |
| Contrato | Protobuf, JSON Schema, Buf |
| Análisis e IA | Python 3.12, solo lectura sobre réplica; acciones por MCP |
| Caras | TypeScript, React, Tailwind, Storybook |
| Web y consola | React + Vite |
| Móvil | Expo (React Native) con SQLite local |
| Escritorio | Tauri |
| Textos de la interfaz | `@nexusthrive/i18n` con español e inglés |
| Integraciones | n8n; correo transaccional con Resend, Postmark o Amazon SES |
| Identidad | Clerk para autenticar; el kernel autoriza |
| Monorepo | `go.work`, pnpm workspaces, Turborepo, Taskfile |
| Calidad | GitHub Actions, `go vet`, `staticcheck`, ESLint, Prettier |
| Local | Docker Compose (`infra/docker/compose.yaml`) |

## Decisiones relacionadas

- [ADR-0002 · Lenguajes por capa](../05-decisiones/0002-lenguajes-por-capa.md)
