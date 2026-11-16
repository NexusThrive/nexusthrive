# ADR-0036 · Primera puerta HTTP del kernel: `kernel/gateway`, token verificado y espacio en la ruta

- **Estado:** Aceptada
- **Fecha:** 2026-11-14
- **Responsables:** santy401

## Contexto

M2 (fase B3) construye las puertas del kernel. Antes de fijar el contrato definitivo en `contracts/` (Connect-RPC, MCP, webhooks), hace falta un corte vertical que pruebe las precondiciones del ADR-0035 con código real: la puerta recibe solo el `Engine` y el `Actor` sale únicamente de un token validado. Este ADR recoge las decisiones de ese corte y deja marcado lo que el contrato definitivo debe revisar.

## Decisión

**La puerta vive en `kernel/gateway` y solo importa `execution` y `model`.** No ve el pool ni `kernel/state`; la prueba de arquitectura lo sigue garantizando. Lo que necesita de la base lo pide al motor: `Engine.ResolveActor` (identidad verificada a participante activo del espacio) y `Tx.ListBalances` (lectura del libro con `kernel.ledger.read`).

**El espacio va en la ruta por su id (`/v1/spaces/{space}/...`), no por `slug`.** Resolver un `slug` exigiría leer `kernel.spaces` antes de fijar un espacio, y la política de aislamiento lo impide a propósito. Resolver `slug` a id necesita una función de directorio con su propia migración y su ADR; queda como seguimiento.

**Token: solo RS256, verificado en la puerta.** Se rechazan `alg` distintos (incluidos `none` y los simétricos), `kid` ausente, firma inválida, `iss` distinto, `azp` distinto cuando está configurado, `exp` ausente o vencido y `nbf` futuro, con 5 segundos de holgura. Todo fallo responde igual (`401`, sin causa). Las llaves vienen de un `KeySource`: `RemoteKeys` (JWKS de Clerk, con caché; una `kid` desconocida provoca como mucho una recarga cada 30 segundos y, si el emisor cae, se siguen usando las llaves en caché) o `StaticKeys` (pruebas y desarrollo).

**La identidad sin participante en el espacio no obtiene nada.** `ResolveActor` responde `permission_denied` con un único mensaje, exista o no el espacio, para no revelar qué espacios o identidades existen. En M2 no se crea la identidad en el primer ingreso.

**Operaciones del corte, en el espacio de nombres `ledger`** (el prefijo `kernel` está reservado al motor y obliga a que los intercambios y eventos lleven el prefijo de su operación): `ledger.transfer` (`money`, idempotente: un intercambio que suma cero, un evento y su fila de salida en una transacción), `ledger.balances` y `ledger.events` (`read`). El contrato `kernel.v1` definitivo debe decidir cómo se nombran y se registran las operaciones del kernel expuestas.

**Idempotencia en la puerta.** `Idempotency-Key` es obligatoria en `POST /transfers`. La repetición con la misma clave y el mismo cuerpo devuelve la respuesta guardada con `Idempotent-Replayed: true`; la misma clave con otro cuerpo es `409 idempotency_conflict`.

**Errores.** Los tipados conservan su mensaje y se traducen a estado HTTP (`invalid_input` 400, `permission_denied` 403, `not_found` 404, `idempotency_conflict` 409, `rule_violation` 422). Cualquier otro error, un pánico recuperado incluido, se registra y responde un `500` genérico.

**Ayudas de desarrollo.** Con `NEXUSTHRIVE_DEV=1` la API firma tokens con una llave propia (`DevIssuer`), sirve la página de la consola local y abre el pool con `SET ROLE` al rol limitado, para que el aislamiento por fila aplique igual que en producción. Fuera de ese modo no existe ninguna de las tres rutas (`/`, `/dev/session`, `/.well-known/jwks.json`) y la API exige `CLERK_ISSUER` y `CLERK_JWKS_URL`.

## Consecuencias

- Las precondiciones del ADR-0035 quedan probadas con código: la puerta no ve el pool y no hay otra forma de construir un `Actor` que un token verificado.
- Queda pendiente, y no se oculta: Connect-RPC y los contratos en `contracts/`, MCP, webhooks, llaves por espacio, límites de tasa, el verificador SCRAM del rol de la API, y la prueba de humo con un token real de Clerk. La interfaz local está en español fijo; pasa a `@nexusthrive/i18n` cuando exista la consola.
- La numeración de ADR propuesta por el backlog de M2 (0036 a 0044) cambia: este ADR toma el 0036 y los demás se renumeran al escribirse.
