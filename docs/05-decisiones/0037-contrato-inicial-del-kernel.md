# ADR-0037 · Contrato inicial de `kernel.v1` y generación local

- **Estado:** Aceptada
- **Fecha:** 2026-11-15
- **Responsables:** santy401
- **Aceptación:** santy401, 2026-11-15

## Contexto

HU-gateway-01 debe cerrar el contrato antes de HU-gateway-08 (generación) y HU-gateway-09 (operaciones). El ADR-0036 ya ocupa el número que proponía el backlog: describe el corte HTTP con `ledger.transfer`, `ledger.balances` y `ledger.events`, no el contrato definitivo.

El motor reserva `kernel*`: las extensiones no pueden registrar esas operaciones y ni `Run` ni `RunIdempotent` aceptan ejecutarlas con un manejador aportado por el llamador. Relajar esa protección permitiría atribuir hechos del kernel a código externo. A su vez, HU-gateway-09 exige escribir intercambio, compromiso, evento y outbox de forma atómica; una transferencia aislada no satisface ese criterio.

Esta decisión desarrolla el ADR-0004 para M2. Python permanece pospuesto por el recorte del ADR-0018. El responsable aceptó el ADR el 2026-11-15; queda habilitada HU-gateway-08.

## Decisión

### Operaciones y autoridad

El paquete Protobuf es `kernel.v1`. Incluye inicialmente dos métodos unarios de `KernelService`:

| Método RPC | Operación del motor | Permisos, todos obligatorios | Capacidad | Riesgo | Idempotencia |
|---|---|---|---|---|---|
| `WriteExchange` | `kernel.exchange.write` | `kernel.exchange.write`, `kernel.commitment.write` | Ninguna (`""`) | `money` | Obligatoria |
| `ReadEvents` | `kernel.event.read` | `kernel.event.read` | Ninguna (`""`) | `read` | Sin registro de idempotencia |

Las opciones de método del contrato declaran nombre de operación, lista de permisos, capacidad, riesgo e idempotencia. HU-gateway-08 genera esas declaraciones; HU-gateway-09 comprueba su correspondencia con el catálogo al construir el motor. No se mantiene un segundo catálogo escrito a mano en la puerta.

El espacio y el actor no son campos de entrada: la puerta selecciona el espacio de la ruta y resuelve el actor desde una credencial validada. La política definitiva de selección del espacio corresponde a HU-gateway-02; este ADR no cambia la ruta por UUID aceptada para el corte del ADR-0036. `request_id` es contexto de llamada validado o generado en servidor, nunca autoridad para elegir actor.

### Escritura: `WriteExchange`

`WriteExchangeRequest` contiene:

| Campo | Tipo | Regla |
|---|---|---|
| `idempotency_key` | `string` | Obligatorio; `^[A-Za-z0-9._:-]{1,128}$` |
| `entries` | Lista de `EntryInput` | De 2 a 100 asientos; suman cero por recurso |
| `memo` | `string` | Opcional; si se envía no contiene solo blancos |
| `commitment` | `CommitmentInput` | Obligatorio en este corte; se crea abierto en la misma transacción |

Cada `EntryInput` lleva `account_id`, `resource_id` (UUID no nulos) y `amount` (decimal exacto como cadena, distinto de cero, con la escala del recurso). No admite `float` ni una clave de idempotencia por asiento o intercambio.

`CommitmentInput` lleva `debtor_participant_id`, `creditor_participant_id`, `resource_id`, `amount`, `due_at` y el campo opcional `starts_at`. Los participantes son distintos, el monto es positivo y las fechas son `google.protobuf.Timestamp` en UTC; `due_at` es obligatorio y no precede al inicio efectivo. Todas las referencias se validan en el espacio de la operación. Esos participantes son partes del compromiso, no el actor que ejecuta.

El servidor fija `kind = kernel.exchange.recorded` para el intercambio y `kind = kernel.commitment.created` para el compromiso. Fija `origin_ref` del compromiso al UUID del intercambio recién creado. Crear el compromiso no lo cumple: no se crean `fulfillments` ni se vincula un asiento a un cumplimiento. No se ofrecen compensaciones, fechas históricas, metadatos arbitrarios ni selección de `kind` en este corte.

`WriteExchangeResponse` contiene `exchange_id`, `commitment_id` y `event_id`, todos UUID v7 asignados por el servidor. Ese objeto es la respuesta almacenada por el motor (máximo 64 KiB). La repetición devuelve los mismos valores; la señal de repetición viaja como metadato de transporte, fuera del objeto almacenado.

El manejador fijo escribe el intercambio, sus asientos, el compromiso y el evento `kernel.exchange.recorded` (versión `1`, asunto igual al UUID del intercambio), con su outbox, dentro de la transacción idempotente. El payload del evento contiene `exchange_id` y `commitment_id`; su JSON Schema vive en `contracts/events/kernel.exchange.recorded.v1.json`. El actor y `request_id` los impone `Tx`. El registro de idempotencia confirma en esa misma transacción; cualquier fallo revierte todo.

La clave viaja en el contrato. Para compatibilidad con JSON/HTTP, `Idempotency-Key` puede aportar el mismo valor: si aparecen ambos deben coincidir; ausencia de ambos o discrepancia es `invalid_input`. La puerta conserva los bytes JSON recibidos en `Call.Request`, sin reconstruirlos desde el mensaje decodificado; el motor aplica la huella canónica del ADR-0026. El mismo objeto JSON con orden o espacios distintos conserva su huella; cambiar valores representados como cadenas puede cambiarla. No se promete equivalencia entre `"1"` y `"1.00"`.

El adaptador del SDK utiliza Connect con JSON para este corte; las solicitudes binarias a la operación idempotente se rechazan antes de ejecutar. Habilitarlas requerirá decidir y probar una representación canónica que no debilite el ADR-0026. Esto evita prometer soporte binario mientras `Call.Request` exige JSON.

### Lectura: `ReadEvents`

`ReadEventsRequest` contiene `from_seq` (`int64`, no negativo) y `limit` (`int32`, entre 1 y 1000). Devuelve `ReadEventsResponse.events`, ordenados por `seq` ascendente, con `seq >= from_seq` del espacio autorizado, conforme a la lectura inclusiva existente. Una página sin datos devuelve una lista vacía.

Cada evento devuelve `id`, `seq`, `space_id`, `type`, `version`, `subject_ref`, `payload` (objeto JSON), `actor_kind`, `actor_participant_id`, `request_id` y `occurred_at`. UUID y decimales son cadenas; `seq` usa la representación JSON de `int64` de Protobuf para evitar pérdida de precisión en TypeScript. La versión se guarda separada del tipo, conforme al ADR-0028.

`seq` no ordena las confirmaciones: esta lectura no es una suscripción ni garantiza que avanzar una vez el cursor capture una transacción concurrente atrasada. Los consumidores reanudan con traslape y deduplican por `id`; para entrega duradera usan webhooks desde el outbox. No se expone el reclamo ni los marcadores del outbox por API o MCP.

### Registro y acceso al espacio de nombres reservado

HU-gateway-09 añade ambas declaraciones a las operaciones propias de `execution`. Las implementaciones son métodos específicos `Engine.WriteExchange` y `Engine.ReadEvents`, con entradas de datos y manejadores fijados dentro del motor. La puerta no entrega callbacks para operaciones `kernel*`.

La lógica idempotente existente se extrae a un método privado compartido: el método público `RunIdempotent` mantiene el rechazo de nombres `kernel*`; `WriteExchange` llama la ruta privada con su nombre y manejador fijos. `ReadEvents` usa la ruta privada de lectura, que conserva verificación de permisos y transacción de solo lectura. `NewEngine` sigue rechazando registros externos bajo el prefijo reservado.

`services/api` compone un solo motor; las operaciones propias se registran al construirlo. Las extensiones siguen usando su registro externo. No se crea `kernel/extension` ni se concede acceso a `state` o al pool desde la puerta. El corte `ledger.*` no se renombra en la base ni se borran sus eventos; su sustitución en la puerta se realiza en HU-gateway-10.

### Errores

| Código del dominio | Escritura | Lectura | Código Connect |
|---|---|---|---|
| `invalid_input` | Forma, clave o entrada inválidas | Cursor o límite inválidos | `invalid_argument` |
| `permission_denied` | Sin alguno de los permisos o actor resoluble | Sin permiso o actor resoluble | `permission_denied` |
| `not_found` | Referencia ausente del espacio autorizado | No se usa para una página vacía | `not_found` |
| `rule_violation` | Conservación, escala o restricción del modelo | Invariante incumplida | `failed_precondition` |
| `space_suspended` | Espacio sin autorización para escribir | No bloquea por sí solo una lectura | `failed_precondition` |
| `idempotency_conflict` | Clave vigente con otra huella | No aplica | `already_exists` |
| `retryable` (nuevo) | Transacción abortada con SQLSTATE `40001` o `40P01` | Mismos abortos | `aborted` |

`retryable` indica que no hubo confirmación; el cliente puede repetir con la misma clave y cuerpo. El servidor no reintenta automáticamente. HU-gateway-08 declara el código y HU-gateway-09 añade su clasificación con pruebas (seguimientos 33 y 64).

Los errores tipados viajan como detalle `OperationError` con `code` y mensaje público. Los fallos de autenticación son `unauthenticated`; límites y plazos son `resource_exhausted` y `deadline_exceeded`. Un error desconocido o `PanicError` produce `internal` genérico, sin causa SQL, mensaje interno ni pila. La puerta no transforma un fallo de infraestructura en `rule_violation`.

### Generación local y compatibilidad

HU-gateway-08 sustituye los plugins remotos por ejecutables locales fijados y preparados antes de `task gen`:

| Herramienta | Versión inicial propuesta | Salida |
|---|---|---|
| `buf` | `v1.50.0` (ya fijada en el repositorio) | Compilación, lint y compatibilidad |
| `google.golang.org/protobuf/cmd/protoc-gen-go` | `v1.36.5` | `kernel/gen` |
| `connectrpc.com/connect/cmd/protoc-gen-connect-go` | `v1.18.1` | `kernel/gen` |
| `@bufbuild/protoc-gen-es` | `2.2.3` | `packages/sdk/src/gen` |

Las versiones se fijan en herramientas del repositorio y en el lockfile de pnpm, sin rangos ni `latest`. Los runtimes generados deben usar versiones compatibles con sus generadores. `buf.gen.yaml` usa entradas `local`, `paths=source_relative` para Go y `target=ts` para TypeScript. Python queda fuera de M2; los consumidores Python usan las puertas disponibles.

La preparación puede descargar herramientas desde los registros de Go y npm. Con herramientas y dependencias preparadas, `task gen` no usa la red ni ejecuta plugins en `buf.build`. Dos ejecuciones consecutivas producen los mismos archivos; CI comprueba que regenerar no deja diferencias, incluidos archivos nuevos. Las versiones propuestas se validan juntas en HU-gateway-08 antes de fijarlas en código.

CI ejecuta `buf lint` con `STANDARD` y `buf breaking` con `FILE` contra el contrato del commit base del PR. Si la base aún no contiene `.proto`, se registra ese único arranque sin comparación y se ejecutan lint y generación; una base con contratos nunca omite la comparación. Los cambios incompatibles requieren `kernel.v2`; los eventos usan una versión nueva de su JSON Schema. El detalle de permisos debe conservarse también en los descriptores que consumirá la generación MCP de solo lectura.

## Alternativas consideradas

| Alternativa | Motivo para descartarla |
|---|---|
| Conservar `ledger.*` como contrato definitivo | No expresa la propiedad de operaciones y eventos del kernel; deja sin resolver el contrato `kernel.v1` pedido por M2 |
| Permitir `kernel*` en registros o callbacks externos | Debilita el límite de confianza del ADR-0035 |
| Primera escritura solo de transferencia | Omite el compromiso exigido por HU-gateway-09 |
| Plugins remotos sin versión | La generación depende de un servicio externo y puede cambiar sin un cambio revisado |
| Generar Python en M2 | Amplía el recorte aceptado de B3 |

## Consecuencias

- El contrato inicial cubre conservación, compromiso, memoria, permiso e idempotencia en una sola escritura sin introducir vocabulario de negocio.
- Se requieren cambios internos en `execution`, con pruebas de rechazo de registros y callbacks externos antes de implementar.
- El soporte inicial de transporte para dinero se limita a JSON; la representación binaria queda pendiente de otra decisión.
- No se habilita un catálogo completo: faltan lecturas y escrituras adicionales que llegarán por sus propias historias.
- La aceptación de este ADR desbloquea HU-gateway-08, pero HU-gateway-09 también necesita las decisiones de HU-gateway-02.

## Cumplimiento

1. HU-gateway-08 verifica declaraciones, lint, compatibilidad, generación local repetible y JSON Schema del evento.
2. HU-gateway-09 prueba antes de implementar: atomicidad incluyendo compromiso y outbox, repetición, conflicto, rechazo sin clave, permisos completos, aislamiento y rechazo de callbacks `kernel*`.
3. HU-gateway-10 verifica clave de campo/cabecera, cuerpo original, errores genéricos, rechazo de formato binario para dinero y ausencia de rutas de administración del outbox.
4. `kernel/laws/` y la prueba de arquitectura permanecen obligatorias. Antes de abrir PR se ejecutan `task lint`, `task test` y `task docs:check`; cualquier limitación del entorno se documenta.

## Referencias

- [Contrato](../02-arquitectura/contrato.md), [backlog M2](../product/backlog/m2-puertas-del-kernel.md) y [plan](../product/plan.md).
- [ADR-0004](0004-contrato-unico.md), [ADR-0018](0018-recorte-de-la-etapa-1.md), [ADR-0026](0026-idempotencia-por-registro.md), [ADR-0028](0028-orden-de-eventos.md), [ADR-0035](0035-motor-de-ejecucion.md) y [ADR-0036](0036-primera-puerta-http-del-kernel.md).
- [Configuración de plugins locales de Buf](https://buf.build/docs/configuration/v2/buf-gen-yaml/).
- Versiones de referencia: [protoc-gen-go v1.36.5](https://github.com/protocolbuffers/protobuf-go/releases/tag/v1.36.5), [Connect Go v1.18.1](https://github.com/connectrpc/connect-go/releases/tag/v1.18.1) y [Protobuf-ES v2.2.3](https://github.com/bufbuild/protobuf-es/releases/tag/v2.2.3).
