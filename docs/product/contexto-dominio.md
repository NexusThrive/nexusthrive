# Contexto de dominio: NexusThrive, fase M1 (B2), núcleo del kernel

## Propósito del producto

NexusThrive es un kernel que estructura ecosistemas y los mantiene funcionando. No conoce ningún negocio: solo siete piezas y cinco leyes (`AGENTS.md`, `docs/01-modelo/primitivas.md`, `docs/01-modelo/leyes.md`). Alrededor hay tres capas: plataforma (`platform/`, `apps/console`), distribuciones (`distributions/`, la primera es Thrive Business) y apps sin lógica de negocio (`AGENTS.md`).

La petición de este brief es la fase M1 = B2 de la hoja de ruta (`docs/02-arquitectura/hoja-de-ruta.md`). Criterio de terminado:

- Las cinco leyes tienen pruebas en verde.
- Un intercambio y un compromiso de prueba se escriben en una transacción con su evento y su outbox.
- El aislamiento entre espacios está probado (criterio añadido por la petición; la hoja de ruta lo pone en B6, ver preguntas abiertas).

El alcance MVP de B2 está fijado en `docs/05-decisiones/0018-recorte-de-la-etapa-1.md`:

- **Entra:** primitivas, cinco leyes, espacios, identidad y permisos, libro, compromisos, eventos con outbox, migraciones.
- **Pospuesto:** particionado del libro y reportes de ledger a gran escala. No se construye sin un ADR nuevo.

## Estado real del código (verificado)

**Casi no hay código de kernel todavía.** La rama actual es `chore/HU-urls-adr-01`, no `sprint/S01`. Lo único ejecutable que existe en el kernel:

| Elemento | Ruta | Qué es |
|---|---|---|
| Módulo Go | `kernel/go.mod` | `github.com/nexusthrive/nexusthrive/kernel`, Go 1.24, sin dependencias |
| Paquete raíz | `kernel/doc.go` | Solo el comentario de paquete |
| Prueba de arquitectura | `kernel/architecture_test.go` | Verifica que no se importen `platform`, `distributions` ni `services`, y que ningún identificador use el vocabulario de negocio prohibido (lista en el archivo). Incluye una prueba de que la regla no es vacía |
| README del kernel | `kernel/README.md` | Lista los paquetes planeados, ninguno existe |
| Contratos | `contracts/buf.yaml`, `contracts/buf.gen.yaml` | Solo configuración de buf. `contracts/proto/` y `contracts/events/` solo tienen README, sin `.proto` ni JSON Schema |
| Binario | `services/api/cmd/api/main.go` | `main()` vacío; el cableado queda para una fase posterior |
| Plataforma | `platform/doc.go` | Solo comentario de paquete |
| Infraestructura local | `infra/docker/compose.yaml` | PostgreSQL 17 y Redis 7, sin migraciones ni scripts |
| Tooling | `Taskfile.yml`, `tools/` | `task test`, `lint`, `gen`, `infra:up`, `docs:check`. `task gen` y `lint:contracts` no hacen nada mientras no haya `.proto` |
| CI | `.github/workflows/ci.yml` | Valida el nombre de rama y corre solo lo que cambió |

No existen: `kernel/model`, `state`, `capability`, `execution`, `coordination`, `extension`, `laws`, `db`, `gateway` ni migraciones SQL. No hay `sqlc.yaml`, ni herramienta de migraciones elegida, ni Testcontainers, ni `pgx` en `go.mod`. Todo B2 está por construir.

## Usuarios y roles visibles en el código

El código no define ningún rol todavía. Lo que está solo en la documentación (`docs/02-arquitectura/modelo-de-datos/kernel.md`):

- **Identidad** (`identities.provider`): `clerk`, `api_key`, `system`.
- **Participante** (`participants.kind`): `person`, `organization`, `agent`, `system`.
- **Roles por espacio** (`roles.key`, ejemplos): `owner`, `admin`, `reception`. Los roles llevan `permissions text[]` y una marca `system`.
- **Actor de evento** (`events.actor_kind`): `person`, `agent`, `api_key`, `system`.
- **Equipo NexusThrive:** vive en un espacio de tipo `platform` y su acceso a espacios ajenos es temporal, con motivo y auditado (`AGENTS.md` reglas 9 y 10). Pertenece a B4, pero el modelo de `grants` ya prevé `reason` y `expires_at`.

## Glosario del dominio (término -> dónde aparece)

| Término | Significado en el kernel | Dónde aparece |
|---|---|---|
| `Space` | Unidad de aislamiento; tipos `platform`, `business`, `community`, `personal`; puede tener `parent_space_id` | `docs/00-identidad/glosario.md`, `docs/02-arquitectura/modelo-de-datos/kernel.md` |
| `Identity` | Alguien que puede iniciar sesión; única por `(provider, subject)` | `modelo-de-datos/kernel.md`, ADR-0006 |
| `Participant` | Una identidad, o alguien sin cuenta, dentro de un espacio | `docs/01-modelo/primitivas.md`, `modelo-de-datos/kernel.md` |
| `Resource` | Qué tiene valor; `kind`: `money`, `unit`, `time`, `access`; con `scale` | `modelo-de-datos/kernel.md` |
| `Account` | Lugar donde un participante guarda un recurso; clases `asset`, `liability`, `equity`, `income`, `expense` | `modelo-de-datos/kernel.md` |
| `Exchange` | Movimiento de valor; `idempotency_key` única por espacio; `reverses_exchange_id` para compensar | `modelo-de-datos/kernel.md`, ADR-0009 |
| `Entry` | Asiento; `amount` numérico, positivo entra y negativo sale; suma cero por `(exchange_id, resource_id)` | `modelo-de-datos/kernel.md` |
| `Commitment` | Valor prometido; deudor, acreedor, `due_at` obligatorio; estados `open`, `fulfilled`, `canceled`, `expired` | `modelo-de-datos/kernel.md` |
| `Fulfillment` | Aplica un asiento a un compromiso | `modelo-de-datos/kernel.md` |
| `Timer` | Despierta un compromiso en `fire_at`; estados `scheduled`, `fired`, `canceled` | `modelo-de-datos/kernel.md` |
| `Event` | Memoria de solo agregar; `seq` bigint, `type`, `version`, `payload` validado con JSON Schema | `modelo-de-datos/kernel.md` |
| `Outbox` | Entrega pendiente de cada evento a un suscriptor (`webhooks`, `n8n`, `ai`, `internal`) | `modelo-de-datos/kernel.md` |
| `Grant`, `Role`, `Capability` | Permisos por espacio, roles y capacidades activas (`space_capabilities`) | `primitivas.md`, `modelo-de-datos/kernel.md` |
| `idempotency_records` | Respuesta guardada por `space_id` y clave durante 24 horas | `modelo-de-datos/kernel.md` sección 5 |
| `access_audit` | Registro de accesos privilegiados | `modelo-de-datos/kernel.md`, `AGENTS.md` regla 10 |
| Componentes del kernel | `Model`, `State`, `Capability`, `Coordination`, `Execution Engine`, `Extension` | `docs/02-arquitectura/kernel.md` |

## Módulos relacionados con la solicitud

Paquetes planeados en `kernel/README.md` y `docs/02-arquitectura/kernel.md`. Ninguno existe aún.

| Paquete | Responsabilidad en B2 |
|---|---|
| `kernel/model` | Las siete piezas como tipos |
| `kernel/state` | Libro, conservación, eventos y memoria |
| `kernel/capability` | Espacios, identidades, grants y verificación de permiso |
| `kernel/execution` | Transacción única por operación e idempotencia |
| `kernel/coordination` | Timers, despacho del outbox, `scheduler_runs` |
| `kernel/extension` | API de registro para plataforma y distribuciones; las operaciones se traducen a piezas del kernel |
| `kernel/laws` | Pruebas de las cinco leyes, nunca omitidas |
| `kernel/db` | Migraciones y consultas `sqlc` del esquema `kernel` |
| `kernel/gateway` | Es B3. Queda fuera de M1 |

Tablas del esquema `kernel` por pieza (`modelo-de-datos/kernel.md`):

| Pieza | Tablas |
|---|---|
| Participante | `identities`, `participants`, `contact_points` |
| Regla | `roles`, `grants`, `invitations`, `api_keys`, `space_capabilities` |
| Recurso | `resources`, `accounts` |
| Intercambio | `exchanges`, `entries` |
| Compromiso | `commitments`, `fulfillments` |
| Tiempo | `timers`, `scheduler_runs` |
| Evento | `events`, `outbox`, `webhook_endpoints`, `webhook_deliveries`, `notices` |
| Soporte | `idempotency_records`, `usage_counters`, `access_audit`, `files` |
| Proyección | `account_balances` (aparece en el diagrama del libro, no en la tabla resumen) |

## Comportamiento actual relevante (solo lo verificado en el código)

Código ejecutable:

- La única regla que hoy se hace cumplir por código es la de arquitectura: sin importaciones de otras capas y sin vocabulario de negocio en identificadores (`kernel/architecture_test.go`). La prueba analiza identificadores Go; no analiza SQL, literales de cadena ni comentarios.
- `task test` ejecuta `go test` sobre todos los módulos del `go.work` (`Taskfile.yml`). El CI está en `.github/workflows/ci.yml`.

Reglas documentadas, que son la especificación y no comportamiento ya implementado:

1. **Conservación.** Un disparador diferido verifica `sum(entries.amount) = 0` por `(exchange_id, resource_id)` al confirmar (`modelo-de-datos/kernel.md`, ADR-0009).
2. **Memoria.** `events`, `entries` y `fulfillments` no admiten `UPDATE` ni `DELETE`. Las correcciones usan `reverses_exchange_id` (`modelo-de-datos/kernel.md`).
3. **Compromiso.** `due_at` y `status` son obligatorios y `fulfilled_amount <= amount` (`modelo-de-datos/kernel.md`).
4. **Permiso.** Toda fila escrita tiene actor y no hay escrituras anónimas. Toda operación pasa por `Capability` antes de ejecutarse (`docs/01-modelo/leyes.md`, `kernel.md`).
5. **Movimiento.** Al crear o modificar un compromiso con vencimiento se crea o reprograma su `timer` en la misma transacción (`modelo-de-datos/kernel.md`).
6. **Camino de una operación** (`docs/02-arquitectura/kernel.md`): puerta, `Capability`, extensión, `Execution Engine` (transacción e idempotencia), `State` escribe intercambios, compromisos, eventos y outbox, y `Coordination` despacha al confirmar.
7. **Aislamiento.** `space_id` en toda tabla de datos de espacio y RLS como segunda barrera (`docs/02-arquitectura/modelo-de-datos/README.md`, `docs/07-ingenieria/security.md`).
8. **Pruebas.** Prueba antes que implementación, pruebas de leyes nunca omitidas, nombres en inglés descriptivos, datos creados por la propia prueba, integración contra PostgreSQL real con Testcontainers (`docs/07-ingenieria/testing.md`).

## Restricciones técnicas

- **Stack:** Go 1.24, `go.work`, `sqlc` y `pgx`, decimales exactos, errores con `fmt.Errorf("...: %w", err)` (`AGENTS.md`, `docs/02-arquitectura/stack.md`).
- **Base de datos:**
  - PostgreSQL 17 local (`infra/docker/compose.yaml`).
  - Supabase solo como PostgreSQL administrado, sin PostgREST ni Supabase Auth.
  - Pooler solo en modo sesión, porque `pgx` usa sentencias preparadas (ADR-0019).
  - Las migraciones corren antes de recibir tráfico (ADR-0019).
- **Esquemas:** el kernel escribe solo en `kernel`, con rol de base de datos propio (ADR-0010, `AGENTS.md` regla 3).
- **Una operación, una transacción:** libro, compromisos, eventos y outbox juntos o ninguno (`AGENTS.md` regla 4).
- **Datos:**
  - Dinero con `numeric`, nunca `float`.
  - IDs UUID v7.
  - `timestamptz` en UTC.
  - Locale `es-CO`, zona `America/Bogota`, moneda `COP` por defecto.
- **Contrato:**
  - Toda operación declara permiso, capacidad, riesgo (`read`, `write`, `money`) e idempotencia; las de dinero siempre son idempotentes.
  - Los errores tipados son `permission_denied`, `capability_inactive`, `rule_violation`, `not_found`, `already_done`, `invalid_input`, `space_suspended` (`docs/02-arquitectura/contrato.md`, ADR-0004).
  - Los eventos son JSON Schema versionado (`kernel.space.created.v1`).
  - El Go generado va en `kernel/gen` y no se edita a mano.
- **Vocabulario:** nada de lenguaje de negocio en `kernel/` (identificadores, tipos, funciones, campos). Los ejemplos de `kind` con espacio de nombres (`<distribución>.<tipo>`) se usan solo como datos en pruebas, con nombres genéricos. Un `kind` de ejemplo como cadena no lo detecta la prueba actual, pero viola el espíritu de ADR-0001.
- **Fuera de B2:** nada de la columna "Pospuesto" de ADR-0018 y nada de B3 (puertas, Clerk, MCP, webhooks firmados). Regla 11 de `AGENTS.md`.
- **Proceso:**
  - Un cambio significativo requiere ADR antes del código (regla 12).
  - Ramas `feature|fix|chore|hotfix/HU-<slug>-NN`.
  - Commits Conventional Commits en inglés.
  - Un PR por historia con `docs/` actualizado.
  - Antes del PR: `task lint`, `task test`, `task docs:check`.
  - Código, comentarios y pruebas en inglés; `docs/` en español.

## Preguntas abiertas para el equipo

1. **Rama.** El repositorio está en `chore/HU-urls-adr-01` (limpia), no en `sprint/S01`. `sprint/S01` no aparece en el `git log` consultado. ¿Se parte de `sprint/S01` y con qué nombres de rama `feature/HU-kernel-...-NN` se dividen las historias?
2. **Aislamiento en M1.** La hoja de ruta lo asigna a B6, pero esta petición lo exige en M1. ¿Basta con una prueba de integración con RLS activo y dos espacios sobre las tablas de B2, dejando la batería completa para B6?
3. **Herramienta de migraciones.** Ningún documento fija cuál usar (goose, atlas, golang-migrate). Hace falta decidirlo, posiblemente con ADR (regla 12). Tampoco está definido cómo se versionan las migraciones por esquema (`kernel`, `platform`, `business`) ni el rol de base de datos del kernel.
4. **RLS con pooler en modo sesión.** ¿Cómo se pasa el `space_id` a las políticas: `SET LOCAL` por transacción desde `execution/`? Los docs piden RLS pero no definen el mecanismo ni qué rol salta la RLS (acceso de plataforma).
5. **Alcance de tablas en B2.**
   - ADR-0018 incluye "eventos con outbox", pero el esquema agrupa `webhook_endpoints`, `webhook_deliveries` y `notices` con ellos, y las entregas firmadas son B3.
   - También quedan `contact_points`, `invitations`, `api_keys`, `usage_counters`, `files` y `access_audit`.
   - ¿Cuáles entran en M1 y cuáles esperan a B3 o B4?
6. **Estados del compromiso.** `leyes.md` lista tres (`abierto`, `cumplido`, `cancelado`) y el modelo de datos agrega `expired`. ¿Cuál es la fuente de verdad y qué transición produce `expired`?
7. **Primeros contratos.** ¿Qué operaciones `kernel.v1` y qué eventos JSON Schema entran en M1? Hoy `contracts/proto` y `contracts/events` están vacíos. El criterio "un intercambio y un compromiso en una sola transacción" puede probarse a nivel de paquete Go sin contrato, pero la regla 6 de `AGENTS.md` pide declararlo. ¿Se aplaza el `.proto` a B3?
8. **Idempotencia.** `idempotency_records` guarda la respuesta 24 horas. ¿Qué pasa con una repetición con la misma clave y distinto cuerpo (conflicto o error `already_done`)? No está especificado. Hay además dos claves: `exchanges.idempotency_key` y `idempotency_records`.
9. **Despacho del outbox.** ¿Quién lo consume en M1 si no hay webhooks ni n8n? ¿Basta con el suscriptor `internal` y una prueba de que la fila queda `pending` en la misma transacción?
10. **Movimiento.** ¿El reloj de `Coordination` (proceso en el binario `services/api`, con `scheduler_runs`) entra en M1 o solo la creación del `timer` dentro de la transacción? Para cerrar la ley de movimiento hace falta al menos una prueba que dispare un timer vencido.
11. **Vocabulario en ejemplos.** `docs/02-arquitectura/modelo-de-datos/kernel.md` ilustra `kind` con nombres de distribución y un ejemplo de pago. Para las pruebas del kernel, ¿se acuerda usar tipos neutros (por ejemplo `test.exchange`)?
12. **Pruebas de integración.** Testcontainers está en `docs/07-ingenieria/testing.md`, pero no hay dependencia ni job de CI con Docker. ¿Se usa Testcontainers o el PostgreSQL de `task infra:up`?
13. **Permiso en M1.** El motor de capacidades depende de roles y permisos, pero los permisos concretos del kernel (lista de claves) no están definidos. ¿Quién define el catálogo mínimo?
