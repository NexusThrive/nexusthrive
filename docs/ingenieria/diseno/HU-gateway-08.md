# HU-gateway-08 · Contratos iniciales del kernel

## Propósito y alcance

Implementar el ADR-0037 aceptado: contrato `kernel.v1`, payload del evento y generación local de Go y TypeScript. Las operaciones del motor y el servidor Connect quedan para HU-gateway-09 y HU-gateway-10.

## Cambios

- `policy.proto` declara metadatos de autoridad en opciones de método; `kernel.proto` contiene `WriteExchange`, `ReadEvents` y sus mensajes; `errors.proto` incluye el detalle público y los códigos `idempotency_conflict` y `retryable`.
- El monto se transmite como cadena decimal y el cursor como `int64`. Los mensajes de entrada no aceptan actor, identidad ni espacio afirmados por el cliente.
- El esquema `kernel.exchange.recorded.v1.json` exige identificadores UUID v7 del intercambio y compromiso y rechaza propiedades adicionales.
- `task tools:contracts` prepara los ejecutables fijados en `tools/bin`. `task gen` usa solo esos ejecutables y el plugin ES instalado por pnpm; no descarga herramientas.
- `task contracts:check` compara el contrato con el commit base del PR usando el historial local. El primer contrato sin una base Protobuf se informa expresamente.
- CI añade lint, compatibilidad, doble generación, rechazo de diferencias y archivos nuevos, y pruebas de los contratos. El SDK generado tiene comprobación estricta de tipos.

## Evidencia de validación

Las pruebas se escribieron antes de los contratos: la del evento falló por ausencia del JSON Schema y la de Go por ausencia de archivos generados. Después de implementar pasan:

- Pruebas Go de políticas, permisos, idempotencia declarada, precisión decimal y de cursor, ausencia de autoridad afirmada en las solicitudes y códigos de error requeridos.
- Validación del payload del evento, con referencias válidas y casos inválidos.
- Un cambio real de `amount` a `quantity` en una copia temporal del contrato: `buf breaking` lo rechaza.
- Segunda generación sin diferencias byte por byte, incluidos los nombres de archivos.
- `task lint`, `task test`, `pnpm typecheck` y compilación de todos los módulos. La suite incluye arquitectura y las cinco leyes contra PostgreSQL del proyecto en 55432, sin omisiones.
- `task gen` y el verificador de diagramas con `CI=1`: 58 diagramas renderizados correctamente. Los enlaces locales de la documentación cambiada y `git diff --check` también pasan.

Task se ejecutó mediante `go run github.com/go-task/task/v3/cmd/task@v3.40.1`; para lint y la suite nueva se utilizó `GOTOOLCHAIN=go1.24.7`, conforme a la versión del proyecto. Staticcheck 2025.1.1 no admite el formato de exportación de Go 1.27 instalado en el servidor.

## Límites y dependencias

Generar el contrato no expone nuevas operaciones en producción. El código `retryable` está en el contrato pero su clasificación en el motor pertenece a HU-gateway-09. Esa historia necesita también los ADR de HU-gateway-02. El PR #27 y la integración de HU-gateway-01 siguen pendientes; no se fusionan como parte de esta historia.

## Referencias

- [ADR-0037](../../05-decisiones/0037-contrato-inicial-del-kernel.md).
- [Contrato](../../02-arquitectura/contrato.md).
- [Backlog M2](../../product/backlog/m2-puertas-del-kernel.md).
