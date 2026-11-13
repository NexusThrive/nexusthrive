# Stack

> **Go decide, Postgres recuerda, Python analiza, n8n conecta y React muestra.**

## Kernel
- **Go:** operaciones, reglas, permisos, orquestación. Un solo binario, concurrencia nativa.
- **PostgreSQL:** la verdad. Intercambios, compromisos y eventos.
- **sqlc:** SQL tipado desde Go, sin ORM.
- **Redis:** caché y sesiones.
- **Outbox en Postgres:** señales en la misma transacción. **NATS JetStream** cuando el volumen lo pida.

## Contrato
- **Protobuf + Connect-RPC:** operaciones.
- **JSON Schema:** eventos.
- **Buf:** generación de código y control de cambios incompatibles.

## Análisis e IA
- **Python:** reportes, predicciones, agentes. Lee de una réplica; actúa por MCP.

## Caras
- **TypeScript + React.**
- **Tailwind:** estilos.
- **Storybook:** biblioteca de componentes documentada y probada aparte.
- **Web:** React + Vite.
- **Móvil:** Expo (React Native) con SQLite local y sincronización. Funciona sin internet.
- **Escritorio:** Tauri (Windows, macOS, Linux).

## Integraciones
- **n8n:** WhatsApp, correo, SMS, push, Telegram, Google, Shopify, marketplaces.
- **Correo transaccional:** Resend, Postmark o Amazon SES.

## Identidad
- **Clerk:** prueba quién eres y emite el token. El kernel decide qué puedes hacer.

## Herramientas del monorepo
- **Go workspaces** para el kernel y las extensiones.
- **pnpm workspaces** para las caras y los SDK.
- **Docker** para correr todo igual en local y en producción.
- **Taskfile:** un solo comando para levantar todo (`task dev`).
- **GitHub Actions:** CI que solo corre lo que cambió.
