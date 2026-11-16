# HU-gateway-09 · Operaciones iniciales del motor

## Propósito y alcance

Implementar en `execution.Engine` las operaciones del ADR-0037, con los ADR-0038 y 0039 aceptados como decisiones previas. La selección por slug y la función de directorio siguen perteneciendo a HU-gateway-12; no se añaden permisos ni migraciones en esta historia.

## Implementación

- `NewEngine` registra las políticas de las operaciones desde las opciones de método del contrato generado. Valida permisos, riesgo e idempotencia contra el catálogo existente. No se crea otro catálogo en la puerta.
- `Engine.WriteExchange` recibe `Call.Request` original. Lo valida y decodifica con Protobuf JSON sin aceptar propiedades desconocidas. No recibe un manejador ni una segunda representación de los datos que pueda diferir de la huella idempotente.
- La clave puede venir de `Call.IdempotencyKey` o del campo del contrato; si ambas existen deben coincidir. La ruta privada idempotente conserva autorización previa, bloqueo, huella y respuesta almacenada. Los métodos públicos con callbacks siguen rechazando `kernel*`.
- Un manejador fijo registra intercambio y asientos, compromiso abierto con `origin_ref` del intercambio, evento `kernel.exchange.recorded` v1 y outbox interno. El registro de idempotencia confirma en la misma transacción. La respuesta contiene los tres identificadores generados, sin indicar repetición dentro del cuerpo almacenado.
- `Engine.ReadEvents` ejecuta un manejador fijo de lectura con el permiso del contrato, cursor inclusivo y aislamiento de la transacción.
- SQLSTATE `40001` y `40P01` se traducen a `retryable`, sin reintentos automáticos. `42501` sigue siendo un error de infraestructura. El seguimiento de M1 que motiva el código es el 33; el 64 corresponde al despacho de eventos y sigue pendiente de webhooks.

`services/api` ya compone un único motor con `NewEngine`; este constructor incorpora las operaciones propias nuevas. El cableado conserva las operaciones `ledger.*` del corte HTTP mientras HU-gateway-10 implementa la puerta Connect. No se exponen operaciones incompletas ni se crea `kernel/extension`.

## Pruebas

Las pruebas se escribieron antes de los métodos: fallaron al compilar por ausencia de `WriteExchange` y `ReadEvents`. Después comprueban:

- Escritura de intercambio, dos asientos, compromiso, evento, outbox y registro de idempotencia; referencias de la respuesta y vínculo del compromiso al intercambio.
- Repetición sin nuevas filas y respuesta idéntica; clave reutilizada con otro cuerpo rechazada.
- Rechazo cuando falta cualquiera de los dos permisos, sin escritura.
- Fallo de compromiso después del intercambio que revierte toda la operación.
- Rechazo de montos inválidos, exceso de escala, intercambio no conservado, compromiso o plazo ausentes y claves inconsistentes; la misma clave sigue disponible tras cada fallo.
- Clave del contrato aceptada sin cabecera, lectura aislada entre espacios y rechazo de callbacks externos bajo los nombres reservados.
- Clasificación reintentable de abortos de transacción sin disfrazar falta de privilegios.

## Referencias

Validación local: `task lint`, `task test` (incluidas arquitectura y las cinco leyes) y compilación de todos los módulos pasan con Go 1.24.7. Los fallos iniciales de compilación de las pruebas y su ejecución posterior contra PostgreSQL en 55432 constan en la validación de la historia. La integración en el sprint sigue pendiente de las historias previas.

- [ADR-0037](../../05-decisiones/0037-contrato-inicial-del-kernel.md), [ADR-0038](../../05-decisiones/0038-resolucion-de-actor-y-espacio.md) y [ADR-0039](../../05-decisiones/0039-identidad-en-el-primer-ingreso.md).
- [Contrato](../../02-arquitectura/contrato.md) y [backlog M2](../../product/backlog/m2-puertas-del-kernel.md).
