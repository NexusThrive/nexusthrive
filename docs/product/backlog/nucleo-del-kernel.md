# Backlog: Núcleo del kernel (M1 = B2)

Funcionalidad: `docs/product/funcionalidades/nucleo-del-kernel.md`. Brief: `docs/product/contexto-dominio.md`. Prioridad: Must.

## Resumen

- Historias: 15, todas listas. P1 y P2 se resolvieron con las propuestas del analista (ver preguntas).
- Rama base de integración: `sprint/S01`. Ramas de tarea: `feature/HU-kernel-<tema>-NN`, igual al ID de la historia (patrón `^(feature|fix|chore|hotfix)/HU-[a-z0-9]+(-[a-z0-9]+)*-[0-9]{2}$`). Un PR por historia, con `docs/` actualizado.
- Tamaños: S = 1, M = 10, L = 4. Ninguna es XL.

### Decisiones del responsable aplicadas (no se reabren)

1. El catálogo mínimo de permisos lo define este backlog en HU-kernel-permissions-06; el responsable lo aprueba en la revisión del PR.
2. `scheduler_runs` y el proceso programador quedan fuera de M1. M1 crea el timer con el compromiso y ofrece una operación que reclama timers vencidos con reloj inyectable (HU-kernel-timers-12).
3. Rama base `sprint/S01`; IDs con prefijo `HU-kernel-`.
4. Solo se crean tablas que use una operación de M1. `access_audit` se crea con los grants (HU-kernel-grants-05) por la regla 10 de `AGENTS.md`. No se crean `contact_points`, `invitations`, `usage_counters`, `api_keys`, `files`, `webhook_*`, `notices` ni `scheduler_runs`.
5. HU-kernel-migrations-01 escribe el ADR-0024 (herramienta de migraciones). HU-kernel-testinfra-02 actualiza `docs/02-arquitectura/hoja-de-ruta.md` (el aislamiento entra en M1) y `docs/07-ingenieria/testing.md` (PostgreSQL de `task infra:up` y servicio de CI en lugar de Testcontainers).
6. Pruebas primero en todas las historias. Las pruebas de las leyes viven en `kernel/laws` y nunca se omiten.

### Orden y dependencias

```
01 migrations -> 02 testinfra -> 03 spaces -> 04 participants -> 05 grants -> 06 permissions
03 -> 07 resources -> 08 exchanges
03 -> 09 events -> 10 outbox
07 + 04 + 08 -> 11 commitments -> 12 timers
06 + 08 + 09 + 10 + 11 -> 13 execution -> 14 idempotency
todas -> 15 laws (cierre)
```

### Preguntas abiertas (solo las que bloquean)

| ID | Pregunta | Bloquea | Propuesta del analista |
|---|---|---|---|
| P1 | Al vencer un compromiso, ¿el kernel escribe un evento (`kernel.commitment.expired`, versión 1) en la misma transacción? ¿Un compromiso `expired` admite cumplimiento tardío? | HU-kernel-timers-12 | Sí escribe el evento, para que la ley de memoria cubra el cambio de estado. No admite cumplimiento tardío; cualquier cobro posterior se modela con un compromiso nuevo. |
| P2 | Repetir una clave de idempotencia con otro cuerpo: la lista de errores tipados de `docs/02-arquitectura/contrato.md` no tiene un código para esto. ¿Se usa `invalid_input` o se añade un código nuevo (por ejemplo `idempotency_conflict`) con su actualización de ADR-0004 y del contrato? | HU-kernel-idempotency-14 | Añadir `idempotency_conflict`, porque `already_done` significa "ya se hizo" y `invalid_input` sugiere un error de formato. Requiere ADR o enmienda de ADR-0004 antes del código. |

### Supuestos documentados (no bloquean, el responsable puede objetarlos en el PR)

- A1. El parámetro de sesión que lleva el espacio se llama `kernel.space_id` y se fija con `SET LOCAL` (mecanismo pedido por la definición; el nombre es nuevo).
- A2. `identities` no tiene `space_id`. No es dato de espacio; solo es visible a través de participantes del espacio actual (regla nueva, ver HU-kernel-participants-04).
- A3. La creación de un espacio es una operación interna del kernel (bootstrap) ejecutada por un actor `system`, porque aún no existe un espacio donde verificar permisos. La creación pública pertenece a B4.
- A4. Los IDs UUID v7 se generan en Go, no en la base de datos (PostgreSQL 17 no trae generador nativo; fuente: `infra/docker/compose.yaml`).
- A5. Los eventos de M1 se validan en Go sin JSON Schema publicado (definición, supuestos).
- A6. Los `kind`, `type` y códigos de prueba usan solo nombres neutros (`test.exchange`, `test.commitment`, `COP`/`UND` solo como códigos de recurso).

---

## Convenciones comunes a todas las historias

Cada historia las hereda y no las repite en detalle.

- **Pruebas primero** (`AGENTS.md`, sección Testing): el PR muestra, en commits anteriores a la implementación, las pruebas fallando. Las pruebas de ley van en `kernel/laws` sin `t.Skip`.
- **Pureza** (`AGENTS.md` regla 1; `kernel/architecture_test.go`): ningún identificador Go con vocabulario de negocio; la prueba de arquitectura sigue en verde.
- **Estilo**: código, identificadores, comentarios, pruebas y mensajes de commit en inglés; `docs/` en español. Errores con `fmt.Errorf("context: %w", err)`.
- **Pruebas de integración** contra PostgreSQL real (desde HU-kernel-testinfra-02), con datos creados por la propia prueba.
- **Cierre de PR**: `task lint`, `task test` y `task docs:check` en verde; documentación de la historia actualizada en el mismo PR.
- **Datos**: dinero con `numeric`, nunca `float`; IDs UUID v7; `timestamptz` en UTC (`AGENTS.md`, Code conventions).

---

## HU-kernel-migrations-01 · Crear el esquema kernel con una sola orden

- **Estado:** Terminada. **Tamaño:** M. **Rama:** `feature/HU-kernel-migrations-01`.
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero decidir la herramienta de migraciones y poder crear el esquema `kernel` desde cero con una sola orden,
  para que cada historia siguiente agregue tablas de forma reproducible en local y en CI.
- **Criterios de aceptación:**
  1. Dado que existe `docs/05-decisiones/0024-*.md` con estado Aceptado, cuando se revisa, entonces compara al menos goose, atlas y golang-migrate, elige una, explica cómo se versionan las migraciones por esquema (`kernel`, `platform`, `business`) y confirma que las migraciones son SQL puro. El ADR se commitea antes que cualquier código de migración.
  2. Dado el PostgreSQL 17 de `task infra:up` sin esquema `kernel`, cuando se ejecuta `task db:migrate`, entonces existe el esquema `kernel` y la tabla de control de versiones de la herramienta, y el comando termina con código 0.
  3. Dado un esquema `kernel` ya migrado, cuando se ejecuta `task db:migrate` otra vez, entonces no cambia nada y termina con código 0.
  4. Dada una migración con SQL inválido, cuando se ejecuta `task db:migrate`, entonces termina con código distinto de 0, el mensaje identifica el archivo y no queda aplicada a medias (la migración corre en una transacción).
  5. Dado el CI, cuando corre el job de migraciones sobre una base vacía, entonces llega al mismo estado que en local.
- **Reglas de negocio:**
  - Un cambio significativo requiere ADR antes del código (`AGENTS.md` regla 12).
  - Migraciones en SQL puro, una sola orden (definición, Incluido y criterio 6).
  - Las migraciones corren antes de recibir tráfico (`docs/05-decisiones/0019-alojamiento-y-entornos.md`).
  - El kernel solo escribe en el esquema `kernel` (`AGENTS.md` regla 3; ADR-0010).
  - Número de ADR: 0024 (responsable). Nuevo, no existe herramienta en el código hoy (`contexto-dominio.md`, estado real).
- **Dependencias:** ninguna.
- **Fuera de alcance:** tablas de negocio del kernel (historias siguientes), migraciones de `platform` y `business`, roles de base de datos (HU-kernel-spaces-03), Redis.

---

## HU-kernel-testinfra-02 · Probar contra PostgreSQL real en local y en CI

- **Estado:** Terminada. **Tamaño:** M. **Rama:** `feature/HU-kernel-testinfra-02`.
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero un arnés de pruebas de integración que use el PostgreSQL de `task infra:up` y un servicio PostgreSQL en CI,
  para que toda historia posterior pruebe contra una base real sin depender de Docker dentro de las pruebas.
- **Criterios de aceptación:**
  1. Dado `task infra:up` activo, cuando se ejecuta `task test`, entonces las pruebas de integración se conectan al PostgreSQL local, aplican las migraciones y pasan.
  2. Dada una prueba de integración, cuando termina, entonces la base queda sin los datos que creó (cada prueba usa su propia base temporal o su propio esquema, y la elimina), de modo que dos pruebas en paralelo no se ven.
  3. Dado PostgreSQL no disponible, cuando se ejecuta una prueba de integración, entonces falla con un mensaje que dice que falta ejecutar `task infra:up`. No se omite en silencio (`t.Skip` prohibido).
  4. Dado el workflow `.github/workflows/ci.yml`, cuando corre un PR que toca `kernel/`, entonces un servicio PostgreSQL 17 está disponible y las pruebas de integración corren contra él. El nombre de rama sigue validándose.
  5. Dado `docs/07-ingenieria/testing.md`, cuando se lee, entonces ya no menciona Testcontainers para integración y describe PostgreSQL local y servicio de CI.
  6. Dado `docs/02-arquitectura/hoja-de-ruta.md`, cuando se lee la fila de B2 y de B6, entonces B2 incluye la prueba de aislamiento sobre las tablas del kernel de M1 y B6 conserva la batería completa. `task docs:check` pasa.
- **Reglas de negocio:**
  - Pruebas de integración contra PostgreSQL real, datos creados por la prueba (`docs/07-ingenieria/testing.md`).
  - Decisión del responsable: PostgreSQL de `task infra:up` y servicio de CI, no Testcontainers.
  - Las pruebas de ley nunca se omiten (`AGENTS.md`, Testing).
  - El aislamiento se adelanta a M1 (definición, Alcance; hoja de ruta fila B6 hoy lo asigna a B6).
- **Dependencias:** HU-kernel-migrations-01.
- **Fuera de alcance:** pruebas de carga, Redis, la batería completa de aislamiento de B6, matriz de versiones de PostgreSQL.

---

## HU-kernel-spaces-03 · Crear espacios aislados con RLS y SET LOCAL

- **Estado:** Terminada. **Tamaño:** L. **Rama:** `feature/HU-kernel-spaces-03`.
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero espacios con un rol de base de datos que no pueda saltarse la RLS y un mecanismo que fije el espacio en cada transacción,
  para que ningún dato cruce entre espacios desde la primera tabla.
- **Criterios de aceptación:**
  1. Dada la migración de la historia, cuando se consulta el catálogo de PostgreSQL, entonces existe la tabla `kernel.spaces` con las columnas `id`, `kind` (`platform`, `business`, `community`, `personal`), `name`, `slug` único, `status` (`provisioning`, `active`, `suspended`, `closed`), `parent_space_id`, `locale` (`es-CO`), `timezone` (`America/Bogota`), `currency` (`COP`), `created_at`, `suspended_at`, `suspended_reason`, `closed_at`.
  2. Dado que existen dos roles de base de datos, cuando se consulta `pg_roles`, entonces el rol dueño de las tablas (migraciones) no es el rol de la API, y el rol de la API tiene `rolbypassrls = false`, `rolsuper = false` y no es dueño de ninguna tabla del esquema `kernel`.
  3. Dados los espacios A y B creados por el bootstrap, cuando el rol de la API abre una transacción, ejecuta `SET LOCAL kernel.space_id = <A>` y lista `kernel.spaces`, entonces ve solo la fila de A.
  4. Dada una conexión del rol de la API sin `kernel.space_id`, cuando lista `kernel.spaces`, entonces obtiene 0 filas, sin error.
  5. Dada una transacción con `kernel.space_id = A`, cuando intenta insertar o actualizar una fila de `kernel.spaces` con `id = B`, entonces PostgreSQL la rechaza por política RLS.
  6. Dada una transacción que fijó `SET LOCAL` y ya hizo `COMMIT`, cuando la misma conexión se reutiliza sin fijar el espacio, entonces no ve filas (el valor no se filtra entre transacciones; prueba con pool de una sola conexión).
  7. Dada la operación interna `CreateSpace` ejecutada con actor de tipo `system`, cuando recibe `kind`, `name` y `slug` válidos, entonces crea el espacio con `status = provisioning` y devuelve su `id` UUID v7. Con `slug` repetido devuelve `rule_violation` y no escribe nada; con `kind` desconocido devuelve `invalid_input`.
  8. Dado el helper de transacción del kernel, cuando se llama con un `space_id` vacío o nulo, entonces devuelve `invalid_input` y no abre transacción.
- **Reglas de negocio:**
  - RLS por `space_id` en toda tabla con datos de espacio; `space_id` fijado con `SET LOCAL` por transacción (definición, Incluido; `docs/02-arquitectura/modelo-de-datos/kernel.md`, Aislamiento).
  - El rol de la API no tiene `BYPASSRLS` ni es dueño de las tablas (definición, criterio 3). El `SET LOCAL` es válido con el pooler en modo sesión (ADR-0019).
  - Columnas de `spaces` y sus valores: `modelo-de-datos/kernel.md`, sección 1.
  - Para `spaces`, la política compara `id` con el espacio actual (nueva, A1).
  - Nombre `kernel.space_id` (nueva, A1). Bootstrap interno (nueva, A3).
  - La vía de acceso privilegiado del staff no se abre aquí; es de B4 y no se resuelve debilitando la RLS (definición, supuestos).
  - Errores tipados: `rule_violation`, `invalid_input` (`docs/02-arquitectura/contrato.md`).
- **Dependencias:** HU-kernel-testinfra-02.
- **Fuera de alcance:** sedes (`parent_space_id` sin visibilidad jerárquica), transiciones de `status` posteriores a `provisioning`, evento `kernel.space.created.v1`, bypass de RLS del staff, creación pública de espacios (B4).

---

## HU-kernel-participants-04 · Registrar identidades y participantes por espacio

- **Estado:** Terminada. **Tamaño:** M. **Rama:** `feature/HU-kernel-participants-04`.
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero identidades únicas y participantes que pertenezcan a un espacio,
  para que una misma persona pueda actuar en varios espacios sin mezclar sus datos.
- **Criterios de aceptación:**
  1. Dada una identidad con `provider = system` y `subject = s1`, cuando se crea otra con el mismo par `(provider, subject)`, entonces se devuelve `rule_violation` y existe una sola fila (unicidad por `(provider, subject)`).
  2. Dado un `provider` fuera de `clerk`, `api_key`, `system`, cuando se intenta crear la identidad, entonces se devuelve `invalid_input`.
  3. Dada una identidad con participantes en los espacios A y B, cuando se abre una transacción con `kernel.space_id = A`, entonces se ven solo los participantes de A y la identidad (por su participante en A); con el espacio B, ve el participante de B. Una identidad sin participante en el espacio actual no es visible.
  4. Dado un participante con `kind` fuera de `person`, `organization`, `agent`, `system`, cuando se crea, entonces se devuelve `invalid_input` y no se escribe nada.
  5. Dado un participante sin `identity_id`, cuando se crea con `kind = person`, entonces se acepta (alguien sin cuenta) con `status = active`.
  6. Dado un participante de A, cuando una transacción con `kernel.space_id = B` intenta leerlo, actualizarlo o crear uno con `space_id = A`, entonces obtiene 0 filas o un rechazo de RLS.
  7. Dado un participante activo, cuando se archiva, entonces `status = archived` y `archived_at` queda fijado; sigue visible para lectura.
- **Reglas de negocio:**
  - `identities`: `provider`, `subject` único por proveedor, `primary_email`, `display_name`, `last_seen_at`, `disabled_at` (`modelo-de-datos/kernel.md`, sección 1; ADR-0006).
  - `participants`: `space_id`, `identity_id` nulo si no tiene cuenta, `kind`, `display_name`, `legal_name`, `document_type` (`CC`, `NIT`, `CE`, `PP`), `document_number`, `country_code`, `status` (`active`, `archived`), `archived_at` (mismo archivo).
  - Una identidad puede tener un participante por espacio (`modelo-de-datos/kernel.md`, texto de la sección Conceptos).
  - Visibilidad de `identities` solo vía participante del espacio actual (nueva, A2).
  - Un solo participante por `(space_id, identity_id)` cuando `identity_id` no es nulo (nueva; derivada de "una identidad es un participante por espacio"; el responsable puede objetar).
  - Las funciones de este paquete reciben el actor como parámetro; la verificación de permiso llega con HU-kernel-permissions-06 y HU-kernel-execution-13.
- **Dependencias:** HU-kernel-spaces-03.
- **Fuera de alcance:** `contact_points`, `invitations`, `api_keys`, validación de tokens de Clerk (B3), `last_seen_at` actualizado por sesiones.

---

## HU-kernel-grants-05 · Guardar roles, grants, capacidades activas y auditoría de acceso

- **Estado:** Terminada. **Tamaño:** M. **Rama:** `feature/HU-kernel-grants-05`.
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero las tablas de roles, grants, capacidades activas por espacio y `access_audit`,
  para que la ley de permiso tenga datos sobre los cuales verificar y el acceso privilegiado quede auditable.
- **Criterios de aceptación:**
  1. Dado un espacio, cuando se crea un rol con `key = owner`, `name`, `permissions` y `system = true`, entonces queda guardado; crear otro rol con la misma `key` en el mismo espacio devuelve `rule_violation`, y en otro espacio se acepta.
  2. Dado un participante y un rol del mismo espacio, cuando se otorga un grant con `granted_by`, entonces queda con `revoked_at` nulo. Si el rol o el participante son de otro espacio, se devuelve `rule_violation` y no se escribe.
  3. Dado un grant con `expires_at` en el pasado respecto del reloj inyectable, cuando se consulta si está vigente, entonces no está vigente. Con `revoked_at` fijado tampoco.
  4. Dado un grant para un participante cuya `reason` es vacía y cuyo `granted_by` es un participante de un espacio de `kind = platform` (acceso de soporte), cuando se crea, entonces se rechaza con `invalid_input` y se exige `reason` y `expires_at` no nulo.
  5. Dada una capacidad `test.capability` con `status = active`, `source` en `package`, `ai`, `admin`, `code` y `enabled_by`, cuando se registra, entonces queda guardada; con `source` inválido devuelve `invalid_input`; un par `(space_id, capability_key)` repetido devuelve `rule_violation`.
  6. Dado un registro en `kernel.access_audit` con `space_id`, actor, motivo y `request_id`, cuando se intenta actualizarlo o borrarlo, entonces falla (solo agregar).
  7. Dadas las cuatro tablas, cuando una transacción con `kernel.space_id = B` consulta o escribe filas de A, entonces obtiene 0 filas o un rechazo de RLS.
  8. Dado un grant revocado, cuando se vuelve a revocar, entonces devuelve `already_done` y no cambia `revoked_at`.
- **Reglas de negocio:**
  - Columnas y semántica de `roles`, `grants`, `space_capabilities`: `modelo-de-datos/kernel.md`, sección 1 (`reason` obligatorio en accesos de soporte; `source`: `package`, `ai`, `admin`, `code`; `status`: `active`, `suspended`).
  - `access_audit` registra lecturas y escrituras de actores privilegiados con motivo y `request_id` (`modelo-de-datos/kernel.md`, sección 5; `AGENTS.md` regla 10). Se crea aquí por decisión del responsable; su uso real es B4.
  - Acceso privilegiado temporal y con motivo (`AGENTS.md` regla 10). La obligación de `expires_at` y `reason` cuando el otorgante pertenece a un espacio `platform` es una regla nueva derivada de ella.
  - Los dueños de negocio no activan capacidades (`AGENTS.md` regla 9): `space_capabilities.source` solo admite los cuatro orígenes; ninguna operación de este paquete lo expone a un rol de espacio (se verifica en HU-kernel-permissions-06).
  - `access_audit` es de solo agregar (nueva, derivada de la ley de memoria, `docs/01-modelo/leyes.md`).
- **Dependencias:** HU-kernel-participants-04.
- **Fuera de alcance:** el bypass de RLS del staff, las pantallas, la expiración automática de grants por proceso (la vigencia se evalúa al consultar), `invitations`, `api_keys`.

---

## HU-kernel-permissions-06 · Verificar permisos con un catálogo mínimo del kernel

- **Estado:** Terminada. **Tamaño:** M. **Rama:** `feature/HU-kernel-permissions-06`.
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero un catálogo mínimo de permisos y una verificación previa a toda operación,
  para que ninguna operación se ejecute sin actor ni permiso.
- **Catálogo mínimo propuesto (pendiente de aprobación del responsable en la revisión del PR):**

  | Clave | Operación de M1 que la exige | Riesgo |
  |---|---|---|
  | `kernel.participant.read` | listar y leer participantes | read |
  | `kernel.participant.write` | crear y archivar participantes | write |
  | `kernel.role.write` | crear roles | write |
  | `kernel.grant.write` | otorgar y revocar grants | write |
  | `kernel.capability.write` | activar o suspender capacidades del espacio | write |
  | `kernel.resource.write` | definir recursos y abrir o cerrar cuentas | write |
  | `kernel.ledger.read` | leer recursos, cuentas, intercambios y asientos | read |
  | `kernel.exchange.write` | registrar intercambios y compensaciones | money |
  | `kernel.commitment.read` | leer compromisos y cumplimientos | read |
  | `kernel.commitment.write` | crear, cumplir y cancelar compromisos | write |
  | `kernel.event.read` | leer eventos | read |
  | `kernel.timer.process` | reclamar timers vencidos | write |
  | `kernel.outbox.claim` | reclamar filas del outbox | write |

  Los permisos de plataforma (`support.access.read`, etc.) no se incluyen; son de B4.

- **Criterios de aceptación:**
  1. Dado un participante con un grant vigente cuyo rol incluye `kernel.exchange.write`, cuando se verifica ese permiso, entonces el resultado es permitido.
  2. Dado un participante cuyo permiso está solo en `grants.extra_permissions`, cuando se verifica, entonces es permitido.
  3. Dado un participante sin grant, con grant vencido (`expires_at` anterior al reloj inyectable) o revocado, cuando se verifica cualquier permiso, entonces se devuelve `permission_denied`.
  4. Dada una operación sin actor (actor nulo), cuando se verifica, entonces se devuelve `permission_denied` (no existen escrituras anónimas).
  5. Dada una clave que no está en el catálogo, cuando se verifica, entonces se devuelve `invalid_input` y no `permission_denied`, para distinguir un error de código de una falta de permiso.
  6. Dado un espacio con `status = suspended`, cuando se verifica un permiso de tipo `write` o `money`, entonces se devuelve `space_suspended`; los permisos `read` siguen permitidos.
  7. Dada una operación que declara una capacidad `test.capability` y el espacio no la tiene `active`, cuando se verifica, entonces se devuelve `capability_inactive`.
  8. Dado un rol de espacio (por ejemplo `owner`), cuando intenta activar una capacidad con `source = admin`, entonces se devuelve `permission_denied`: la clave `kernel.capability.write` solo se concede a actores de sistema o de plataforma, y el bootstrap no la incluye en el rol `owner`.
  9. Dado el participante de un espacio A con permiso en A, cuando se verifica contra el espacio B, entonces se devuelve `permission_denied`.
  10. Dado un permiso denegado, cuando falla la operación, entonces no queda ninguna fila escrita (verificado con conteo de tablas antes y después, en `kernel/laws`).
- **Reglas de negocio:**
  - Toda operación pasa por `Capability` antes de ejecutarse y no hay escrituras anónimas (`docs/01-modelo/leyes.md`; `docs/02-arquitectura/kernel.md`).
  - Errores tipados `permission_denied`, `capability_inactive`, `invalid_input`, `space_suspended` (`docs/02-arquitectura/contrato.md`, ADR-0004). Cuándo se usa cada uno aquí es regla nueva.
  - Las operaciones declaran riesgo `read`, `write` o `money` (`AGENTS.md` regla 6). El catálogo es nuevo; la forma `<área>.<objeto>.<verbo>` sigue el ejemplo `support.access.read` de `docs/02-arquitectura/plataforma/consola.md`.
  - Las claves del catálogo están en inglés y son neutras (política de idiomas; regla 1).
  - Los dueños de negocio no activan capacidades (`AGENTS.md` regla 9).
  - Las capacidades de prueba usan claves neutras (A6).
- **Dependencias:** HU-kernel-grants-05.
- **Fuera de alcance:** permisos de plataforma y distribuciones, catálogo de capacidades de negocio, expiración de grants por proceso, caché de permisos, contratos en `contracts/` (B3).

---

## HU-kernel-resources-07 · Definir recursos y cuentas

- **Estado:** Terminada. **Tamaño:** S. **Rama:** `feature/HU-kernel-resources-07`.
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero definir recursos y cuentas dentro de un espacio,
  para que el libro tenga dónde guardar valor con decimales exactos.
- **Criterios de aceptación:**
  1. Dado un espacio, cuando se crea un recurso `kind = money`, `code = COP`, `name`, `scale = 2`, entonces queda guardado; repetir `(space_id, code)` devuelve `rule_violation`.
  2. Dado un `kind` fuera de `money`, `unit`, `time`, `access`, cuando se crea, entonces se devuelve `invalid_input`; con `scale` negativo también.
  3. Dado un recurso, cuando se abre una cuenta con `holder_participant_id`, `class = asset` y `code`, entonces queda con `status = open`. Con `class` fuera de `asset`, `liability`, `equity`, `income`, `expense` se devuelve `invalid_input`.
  4. Dada una cuenta con `parent_account_id`, cuando la cuenta madre es de otro recurso o de otro espacio, entonces se devuelve `rule_violation`.
  5. Dada una cuenta de un participante o recurso de otro espacio, cuando se intenta abrir, entonces se devuelve `rule_violation`.
  6. Dada una cuenta con asientos, cuando se cierra, entonces `status = closed` y no admite asientos nuevos (verificado en HU-kernel-exchanges-08).
  7. Dados los espacios A y B, cuando B consulta recursos o cuentas de A, entonces obtiene 0 filas; no puede insertar con `space_id = A`.
  8. Dado el campo `amount` de las tablas del libro, cuando se consulta el catálogo, entonces su tipo es `numeric`.
- **Reglas de negocio:**
  - `resources` (`kind`, `code`, `name`, `scale`) y `accounts` (clases y estados): `modelo-de-datos/kernel.md`, sección 2.
  - Dinero con `numeric`, nunca `float` (`AGENTS.md`).
  - Unicidad `(space_id, code)` de recursos y `scale` no negativo: reglas nuevas derivadas del modelo.
  - Una cuenta cerrada no admite asientos: nueva.
- **Dependencias:** HU-kernel-spaces-03, HU-kernel-participants-04.
- **Fuera de alcance:** `account_balances` (proyección no usada por ninguna operación de M1), conversión entre recursos, particionado del libro (ADR-0018).

---

## HU-kernel-exchanges-08 · Registrar intercambios que conservan el valor y no se reescriben

- **Estado:** Terminada. **Tamaño:** L. **Rama:** `feature/HU-kernel-exchanges-08`.
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero registrar intercambios con asientos que suman cero por recurso y corregirlos solo con intercambios compensatorios,
  para que el valor se conserve y la historia no se reescriba.
- **Criterios de aceptación:**
  1. Dado un intercambio `test.exchange` con dos asientos de +50.00 y -50.00 en el recurso R, cuando se confirma la transacción, entonces se guardan el intercambio y sus asientos con `recorded_at` en UTC.
  2. Dado un intercambio con asientos +50.00 y -49.99 en R, cuando se confirma, entonces la confirmación falla por el disparador diferido y no queda ninguna fila (ley de conservación).
  3. Dado un intercambio con dos recursos R1 y R2, cuando R1 suma cero y R2 suma +1.00, entonces se rechaza; la suma se evalúa por `(exchange_id, resource_id)`.
  4. Dado un intercambio sin asientos o con un solo asiento distinto de cero, cuando se confirma, entonces se rechaza.
  5. Dado un asiento cuya cuenta usa un recurso distinto de su `resource_id`, o una cuenta `closed`, o una cuenta de otro espacio, cuando se registra, entonces se devuelve `rule_violation`.
  6. Dado un monto con más decimales que `resources.scale`, cuando se registra, entonces se devuelve `invalid_input`.
  7. Dado un asiento o un intercambio ya confirmado, cuando se ejecuta `UPDATE` o `DELETE` sobre `entries` (ley de memoria), entonces la base lo rechaza, incluso para el rol dueño de las tablas.
  8. Dado un intercambio E1, cuando se registra uno compensatorio con `reverses_exchange_id = E1` y asientos de signo opuesto, entonces la suma neta por cuenta es 0. Compensar E1 por segunda vez devuelve `already_done`; compensar un intercambio de otro espacio devuelve `not_found`.
  9. Dado un intercambio con `idempotency_key` K en el espacio A, cuando se registra otro con la misma K en A, entonces la restricción única lo rechaza; en el espacio B con la misma K se acepta.
  10. Dados los espacios A y B, cuando B consulta o escribe `exchanges` o `entries` de A, entonces obtiene 0 filas o un rechazo de RLS.
  11. Dado un intercambio sin `actor_participant_id`, cuando se registra, entonces se rechaza (no hay escrituras anónimas).
- **Reglas de negocio:**
  - Conservación: disparador diferido con `sum(entries.amount) = 0` por `(exchange_id, resource_id)` al confirmar (`modelo-de-datos/kernel.md`, Reglas; ADR-0009).
  - Memoria: `entries` sin `UPDATE` ni `DELETE`; las correcciones son intercambios con `reverses_exchange_id` (`modelo-de-datos/kernel.md`; `docs/01-modelo/leyes.md`). Los `exchanges` también son de solo agregar: regla nueva derivada de la misma ley.
  - `exchanges.idempotency_key` única por espacio, `actor_participant_id`, `occurred_at`, `recorded_at` (`modelo-de-datos/kernel.md`, sección 2).
  - Un intercambio solo se compensa una vez y los asientos compensatorios deben invertir los originales: nuevas.
  - Escala y estado de cuenta: reglas nuevas derivadas de HU-kernel-resources-07.
  - Operación de riesgo `money` (siempre idempotente en el motor; ver HU-kernel-idempotency-14).
- **Dependencias:** HU-kernel-resources-07.
- **Fuera de alcance:** `commitment_id` en asientos (descartado en ADR-0033: el vínculo vive solo en `fulfillments`), eventos y outbox (HU-kernel-execution-13), saldos, cobro con Wompi, permisos (se aplican en HU-kernel-execution-13).

---

## HU-kernel-events-09 · Registrar eventos de solo agregar con secuencia

- **Estado:** Terminada. **Tamaño:** M. **Rama:** `feature/HU-kernel-events-09`.
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero un registro de eventos de solo agregar con número de secuencia,
  para que cada hecho del sistema quede en la memoria y pueda reconstruirse en orden.
- **Criterios de aceptación:**
  1. Dado un evento de tipo `kernel.test.recorded`, `version = 1`, `payload` objeto JSON y actor, cuando se registra, entonces recibe `seq` mayor que el último del sistema y `id` UUID v7.
  2. Dados dos eventos registrados en transacciones consecutivas, cuando se comparan, entonces `seq` del segundo es mayor. Un rollback puede dejar huecos en `seq`, pero nunca repetidos.
  3. Dado un evento ya guardado, cuando se ejecuta `UPDATE` o `DELETE`, entonces la base lo rechaza (ley de memoria).
  4. Dado un `type` que no cumple `<namespace>.<name>` en minúsculas, o `version < 1`, o `payload` que no es un objeto JSON, cuando se registra, entonces se devuelve `invalid_input` y no se escribe nada.
  5. Dado un `actor_kind` fuera de `person`, `agent`, `api_key`, `system`, cuando se registra, entonces se devuelve `invalid_input`.
  6. Dado un evento sin actor (`actor_participant_id` nulo y `actor_kind` distinto de `system`), cuando se registra, entonces se rechaza.
  7. Dados los espacios A y B, cuando B lee eventos de A o inserta con `space_id = A`, entonces obtiene 0 filas o un rechazo de RLS.
  8. Dado un espacio, cuando se leen eventos desde `seq = n`, entonces se devuelven en orden ascendente de `seq`, solo del espacio actual.
- **Reglas de negocio:**
  - Columnas de `events` (`seq`, `id`, `type`, `version`, `subject_ref`, `payload`, `actor_participant_id`, `actor_kind`, `request_id`, `occurred_at`) y no admiten `UPDATE` ni `DELETE`: `modelo-de-datos/kernel.md`, sección 4 y Reglas.
  - Los eventos se validan en Go sin JSON Schema publicado (definición, supuestos; A5). El `payload` completo validado por esquema es de B3.
  - Formato del `type` y objeto JSON: regla nueva, para dejar un mínimo mientras no hay contratos.
  - Sin actor no hay escritura (`docs/01-modelo/leyes.md`, ley de permiso). Los eventos del sistema usan `actor_kind = system`.
- **Dependencias:** HU-kernel-spaces-03.
- **Fuera de alcance:** JSON Schema y `contracts/events/` (B3), proyecciones, suscripciones, `webhook_*`, `notices`.

---

## HU-kernel-outbox-10 · Escribir y reclamar filas del outbox sin entrega doble

- **Estado:** Terminada. **Tamaño:** M. **Rama:** `feature/HU-kernel-outbox-10`.
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero que cada evento deje una fila de outbox y que pueda reclamarse sin duplicar la entrega,
  para que B3 construya el despacho sobre una cola ya correcta.
- **Criterios de aceptación:**
  1. Dado un evento registrado para el suscriptor `internal`, cuando se confirma la transacción, entonces existe una fila de `outbox` con `status = pending`, `attempts = 0` y `next_attempt_at` igual al momento del reloj inyectable.
  2. Dado un evento y una fila de outbox escritos en la misma transacción, cuando la transacción hace rollback, entonces no existe ni el evento ni la fila.
  3. Dado un `subscriber` fuera de `webhooks`, `n8n`, `ai`, `internal`, cuando se escribe la fila, entonces se devuelve `invalid_input`.
  4. Dadas 10 filas `pending` con `next_attempt_at <= ahora`, cuando dos reclamantes concurrentes piden lotes de 10, entonces ninguna fila aparece en ambos resultados y la unión cubre las 10 (prueba con `FOR UPDATE SKIP LOCKED` o equivalente, 20 repeticiones).
  5. Dada una fila reclamada, cuando se marca como `sent`, entonces `status = sent` y `delivered_at` queda fijado; marcarla otra vez devuelve `already_done`.
  6. Dada una fila reclamada que falla, cuando se marca `failed`, entonces `attempts` aumenta en 1, `last_error` se guarda y `next_attempt_at` se mueve según el reloj inyectable; al superar 5 intentos pasa a `dead`.
  7. Dada una fila con `next_attempt_at` en el futuro, cuando se reclama, entonces no se entrega.
  8. Dados los espacios A y B, cuando B reclama filas, entonces no obtiene filas de A.
- **Reglas de negocio:**
  - `outbox` (`event_seq`, `subscriber`, `status` `pending`, `sent`, `failed`, `dead`, `attempts`, `next_attempt_at`, `last_error`, `delivered_at`): `modelo-de-datos/kernel.md`, sección 4.
  - En M1 el outbox solo se escribe y se reclama; no hay consumidor (definición, Incluido).
  - Un reclamo concurrente no entrega dos veces la misma fila (definición, criterio 5).
  - Límite de 5 intentos y espaciado de reintentos: **regla nueva**, valores de partida que el responsable puede cambiar en el PR; B3 los puede ajustar al construir el despacho.
  - `outbox` hereda el aislamiento del espacio de su evento: lleva `space_id` (nueva, requerida por la regla de aislamiento de `modelo-de-datos/README.md`).
  - Permiso `kernel.outbox.claim` (HU-kernel-permissions-06).
- **Dependencias:** HU-kernel-events-09.
- **Fuera de alcance:** consumidor, entregas firmadas, n8n, MCP, webhooks, `scheduler_runs`, proceso periódico de reclamo.

---

## HU-kernel-commitments-11 · Crear, cumplir y cancelar compromisos

- **Estado:** Terminada. **Tamaño:** L. **Rama:** `feature/HU-kernel-commitments-11`.
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero compromisos con vencimiento obligatorio que se cumplan con asientos y no superen su monto,
  para que el valor prometido quede registrado y verificable.
- **Criterios de aceptación:**
  1. Dado un deudor, un acreedor y un recurso del mismo espacio, cuando se crea un compromiso `test.commitment` con `amount = 100.00` y `due_at` futuro, entonces queda con `status = open` y `fulfilled_amount = 0`.
  2. Dado un compromiso sin `due_at` o sin `status`, cuando se inserta directamente en la base, entonces se rechaza (restricción `NOT NULL`).
  3. Dado un `amount <= 0`, o `due_at` anterior a `starts_at`, cuando se crea, entonces se devuelve `invalid_input`.
  4. Dado `fulfilled_amount > amount`, cuando se intenta fijar, entonces la base lo rechaza (restricción `CHECK`).
  5. Dado un compromiso abierto de 100.00, cuando se cumple con un asiento de 40.00 del mismo recurso y espacio, entonces existe un `fulfillments` de 40.00, `fulfilled_amount = 40.00` y `status = open`.
  6. Dado un compromiso con 60.00 pendientes, cuando se cumple con 60.00 más, entonces `fulfilled_amount = 100.00`, `status = fulfilled` y `closed_at` queda fijado.
  7. Dado un cumplimiento mayor que el monto pendiente, cuando se intenta, entonces se devuelve `rule_violation` y no se escribe nada.
  8. Dado un asiento de otro recurso o de otro espacio, cuando se intenta aplicar a un compromiso, entonces se devuelve `rule_violation`.
  9. Dado un compromiso `open`, cuando se cancela con `canceled_reason`, entonces `status = canceled` y `closed_at` queda fijado. Cancelar sin motivo devuelve `invalid_input`. Cancelar uno `fulfilled`, `canceled` o `expired` devuelve `already_done`.
  10. Dado un compromiso `canceled`, `expired` o `fulfilled`, cuando se intenta cumplir, entonces se devuelve `rule_violation`.
  11. Dado un `fulfillments` guardado, cuando se ejecuta `UPDATE` o `DELETE`, entonces la base lo rechaza (ley de memoria).
  12. Dados los espacios A y B, cuando B consulta o escribe `commitments` o `fulfillments` de A, entonces obtiene 0 filas o un rechazo de RLS.
- **Reglas de negocio:**
  - Estados `open`, `fulfilled`, `canceled`, `expired` según el modelo de datos (definición, Incluido; `modelo-de-datos/kernel.md`, sección 3). Resuelve la pregunta 6 del brief.
  - `due_at` y `status` obligatorios; `fulfilled_amount <= amount` (`modelo-de-datos/kernel.md`, Reglas).
  - `fulfillments` aplica un `entry_id` a un compromiso y no admite `UPDATE` ni `DELETE` (mismo archivo).
  - Transición a `fulfilled` cuando `fulfilled_amount = amount`; motivo obligatorio al cancelar; solo `open` admite cambios; `amount > 0`: reglas nuevas derivadas del modelo; el responsable puede objetarlas.
  - Los `kind` de prueba son neutros (A6).
  - Permisos `kernel.commitment.write` y `kernel.commitment.read`.
- **Dependencias:** HU-kernel-resources-07, HU-kernel-exchanges-08, HU-kernel-participants-04.
- **Fuera de alcance:** creación del timer (HU-kernel-timers-12), vencimiento, eventos y outbox (HU-kernel-execution-13), `origin_ref` con significado de negocio, mora o intereses.

---

## HU-kernel-timers-12 · Crear timers con el compromiso y vencerlos con reloj inyectable

- **Estado:** Terminada (P1 resuelta: se escribe el evento `kernel.commitment.expired` v1 y no hay cumplimiento tardío). **Tamaño:** M. **Rama:** `feature/HU-kernel-timers-12`.
- **Pregunta que la desbloquea:** P1 (evento al vencer y cumplimiento tardío de un compromiso `expired`).
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero que cada compromiso con vencimiento tenga su timer en la misma transacción y que un reloj inyectable pueda dispararlo,
  para probar la ley de movimiento sin esperar tiempo real.
- **Criterios de aceptación:**
  1. Dado un compromiso creado con `due_at`, cuando se confirma la transacción, entonces existe un `timers` con `status = scheduled`, `commitment_id` y `fire_at = due_at`, escrito en la misma transacción. Si hay rollback, no existe ninguno.
  2. Dado un compromiso con timer `scheduled`, cuando se reprograma `due_at`, entonces el timer pasa a la nueva `fire_at` en la misma transacción, sin crear un segundo timer `scheduled` para el mismo compromiso.
  3. Dado un compromiso que pasa a `fulfilled` o `canceled`, cuando se confirma, entonces su timer pasa a `canceled`.
  4. Dados un timer con `fire_at = T` y un reloj inyectado en T - 1 s, cuando se reclaman los timers vencidos, entonces no se devuelve ninguno.
  5. Dado el reloj adelantado a T + 1 s, cuando se reclaman, entonces el timer pasa a `fired` con `fired_at` fijado, el compromiso pasa a `expired` con `closed_at` fijado y se escribe el evento `kernel.commitment.expired` versión 1 con su fila de outbox, todo en una transacción.
  6. Dado un compromiso ya `fulfilled` cuyo timer quedó `scheduled` por un error previo, cuando se reclama, entonces el timer pasa a `canceled` y el compromiso no cambia.
  7. Dados dos reclamantes concurrentes sobre el mismo timer vencido, cuando ambos corren, entonces solo uno lo dispara (el compromiso pasa a `expired` una sola vez y hay un solo evento).
  8. Dados los espacios A y B, cuando B reclama timers, entonces no ve ni dispara los de A.
  9. Dado un intento de dar `fulfill` a un compromiso `expired`, cuando se ejecuta, entonces se devuelve `rule_violation`.
- **Reglas de negocio:**
  - Al crear o modificar un compromiso con vencimiento se crea o reprograma su timer en la misma transacción (`modelo-de-datos/kernel.md`, Reglas; `docs/01-modelo/leyes.md`).
  - Estados de `timers`: `scheduled`, `fired`, `canceled` (`modelo-de-datos/kernel.md`, sección 3).
  - El reloj es inyectable; el proceso programador y `scheduler_runs` quedan fuera de M1 (decisión del responsable). Nada dispara timers en producción hasta entonces; debe quedar escrito en `docs/02-arquitectura/kernel.md` o en el README de `kernel/`.
  - Un solo timer `scheduled` por compromiso, `fire_at = due_at`, evento al vencer: reglas nuevas.
  - Permiso `kernel.timer.process`.
- **Dependencias:** HU-kernel-commitments-11, HU-kernel-events-09, HU-kernel-outbox-10.
- **Fuera de alcance:** proceso periódico en `services/api`, `scheduler_runs`, recordatorios previos al vencimiento (`signal_type` de negocio), reintentos de timers.

---

## HU-kernel-execution-13 · Ejecutar cada operación en una sola transacción con permiso

- **Estado:** En revisión. **Tamaño:** L. **Rama:** `feature/HU-kernel-execution-13`.
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero un motor de ejecución que verifique permiso y escriba libro, compromiso, evento y outbox juntos o ninguno,
  para que ninguna operación deje estados a medias.
- **Criterios de aceptación:**
  1. Dado un actor con `kernel.exchange.write` y `kernel.commitment.write`, cuando se ejecuta una operación de prueba que registra un intercambio, crea un compromiso, escribe un evento y una fila de outbox `internal`, entonces las cuatro escrituras existen tras el `COMMIT`; el evento lleva el `request_id` de la operación y es el ancla de la trazabilidad: la fila de outbox cuelga del evento por `event_seq` y el `payload` del evento nombra los identificadores del intercambio y del compromiso (reformulado con aprobación del responsable; `exchanges` y `commitments` no tienen columna `request_id`).
  2. Dado el mismo caso, cuando la escritura del compromiso falla, entonces no queda ni intercambio, ni asientos, ni evento, ni outbox.
  3. Dado el mismo caso, cuando la escritura del outbox falla, entonces no queda nada de lo anterior.
  4. Dado el mismo caso, cuando la escritura del intercambio viola la conservación al confirmar, entonces no queda ningún compromiso, evento ni outbox.
  5. Dado un actor sin el permiso, cuando se ejecuta la operación, entonces devuelve `permission_denied` y los conteos de todas las tablas de M1 son iguales antes y después.
  6. Dado un actor nulo, cuando se ejecuta cualquier operación, entonces devuelve `permission_denied` sin escribir.
  7. Dada una operación de riesgo `money`, cuando se declara sin soporte de idempotencia, entonces el motor se niega a registrarla y falla en el arranque (las operaciones de dinero son siempre idempotentes).
  8. Dado un `space_id` en el contexto, cuando la operación corre, entonces la transacción fijó `SET LOCAL kernel.space_id` antes de cualquier consulta; una operación que intenta leer otro espacio obtiene 0 filas.
  9. Dado un espacio `suspended`, cuando se ejecuta una operación `write` o `money`, entonces devuelve `space_suspended` y no escribe.
  10. Dado un pánico dentro de una operación, cuando el motor lo captura, entonces hace rollback y devuelve un error con contexto sin dejar la transacción abierta.
- **Reglas de negocio:**
  - Una operación, una transacción: libro, compromisos, eventos y outbox juntos o ninguno (`AGENTS.md` regla 4; definición, criterio 2).
  - Camino de una operación: `Capability`, extensión, motor, `State`, `Coordination` (`docs/02-arquitectura/kernel.md`).
  - Toda operación declara permiso, capacidad, riesgo e idempotencia; las de dinero siempre son idempotentes (`AGENTS.md` regla 6). En M1 se declara en Go, sin `contracts/` (definición, Excluido).
  - Errores tipados de `docs/02-arquitectura/contrato.md`.
  - `request_id` común a evento y acceso privilegiado: `modelo-de-datos/kernel.md`.
  - El registro por `kernel/extension` queda limitado a lo que use la prueba (regla 11, construir lo que la fase requiere).
- **Dependencias:** HU-kernel-permissions-06, HU-kernel-exchanges-08, HU-kernel-commitments-11, HU-kernel-events-09, HU-kernel-outbox-10.
- **Fuera de alcance:** idempotencia por registro (HU-kernel-idempotency-14), puertas y contratos (B3), registro de extensiones de plataforma o distribuciones.

---

## HU-kernel-idempotency-14 · Repetir una operación con la misma clave sin duplicar

- **Estado:** Lista (P2 resuelta: se añade el código tipado `idempotency_conflict`; se escribe antes el ADR-0026 y se actualizan ADR-0004 y `contrato.md`). **Tamaño:** M. **Rama:** `feature/HU-kernel-idempotency-14`.
- **Pregunta que la desbloquea:** P2 (código de error cuando la misma clave llega con otro cuerpo).
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero que repetir una operación de dinero con la misma clave devuelva la respuesta guardada,
  para que un reintento no duplique asientos.
- **Criterios de aceptación (con la propuesta de P2, error `idempotency_conflict`):**
  1. Dada una operación de dinero con clave K y cuerpo C en el espacio A, cuando se ejecuta por primera vez, entonces se guarda en `idempotency_records` la huella de C y la respuesta, junto con su vencimiento 24 horas después (reloj inyectable), en la misma transacción que el intercambio.
  2. Dada la misma clave K y el mismo cuerpo C, cuando se repite dentro de las 24 horas, entonces devuelve la respuesta guardada y el número de filas de `exchanges` y `entries` no cambia.
  3. Dada la misma clave K con un cuerpo distinto, cuando se repite, entonces devuelve `idempotency_conflict` y no escribe nada.
  4. Dada la clave K en el espacio B con otro cuerpo, cuando se ejecuta, entonces se trata como primera ejecución (la clave es por espacio).
  5. Dos solicitudes concurrentes con la misma clave y cuerpo, cuando corren a la vez, entonces solo una escribe y la otra devuelve la misma respuesta.
  6. Dado un registro con más de 24 horas, cuando se repite la clave, entonces se trata como una operación nueva.
  7. Dada una operación que falla con error de regla, cuando se repite con la misma clave, entonces no hay registro guardado y puede reintentarse (no se cachean fallos).
  8. Dados los espacios A y B, cuando B consulta `idempotency_records` de A, entonces obtiene 0 filas.
  9. Dada una operación `money` sin clave, cuando se ejecuta, entonces devuelve `invalid_input`.
- **Reglas de negocio:**
  - `idempotency_records` guarda la respuesta por `space_id` y clave durante 24 horas (`modelo-de-datos/kernel.md`, sección 5).
  - Repetir con el mismo cuerpo devuelve la respuesta; con otro cuerpo, error tipado (definición, Incluido).
  - Operaciones de dinero siempre idempotentes (`AGENTS.md` regla 6).
  - `exchanges.idempotency_key` única por espacio actúa como segunda barrera (HU-kernel-exchanges-08).
  - Huella del cuerpo, no cacheo de fallos, clave obligatoria: reglas nuevas.
  - Código del error: depende de P2, que puede requerir ADR o enmienda de ADR-0004.
- **Dependencias:** HU-kernel-execution-13.
- **Fuera de alcance:** purga automática de registros vencidos (proceso posterior), idempotencia a nivel de puerta o de cabecera HTTP (B3).

---

## HU-kernel-laws-15 · Cerrar M1 con las cinco leyes y la prueba de extremo a extremo

- **Estado:** Lista (su ejecución espera a las historias previas). **Tamaño:** M. **Rama:** `feature/HU-kernel-laws-15`.
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero una suite consolidada de las cinco leyes y del aislamiento sobre todas las tablas de M1,
  para demostrar que el kernel cumple el criterio de terminado y que B3 puede empezar.
- **Criterios de aceptación:**
  1. Dada la carpeta `kernel/laws`, cuando se ejecuta `go test ./laws/...`, entonces hay al menos una prueba por cada ley (conservación, memoria, compromiso, permiso, movimiento), ninguna con `t.Skip`, y todas pasan. Una prueba de guardia falla si encuentra `t.Skip` o `testing.Short()` en ese paquete.
  2. Dada la prueba de conservación, cuando un intercambio no suma cero por recurso, entonces se rechaza al confirmar. Memoria: `UPDATE` y `DELETE` fallan sobre `events`, `entries` y `fulfillments`, y la corrección se hace con un intercambio compensatorio. Compromiso: sin `due_at` o `status`, o con `fulfilled_amount > amount`, se rechaza. Permiso: sin actor o sin permiso no se escribe nada. Movimiento: con el reloj adelantado el timer se dispara y el compromiso queda `expired`.
  3. Dada una lista de las tablas de datos de espacio de M1 obtenida del catálogo de PostgreSQL, cuando se ejecuta la prueba de aislamiento con dos espacios, entonces para cada tabla con columna `space_id`: A no lee ni escribe filas de B y viceversa, una conexión sin `kernel.space_id` ve 0 filas y tiene RLS activa y forzada. La prueba falla si aparece una tabla con `space_id` sin política.
  4. Dado el rol de la API, cuando se comprueba, entonces no tiene `BYPASSRLS` ni es dueño de tablas.
  5. Dada la prueba de extremo a extremo, cuando se registra un intercambio y se crea un compromiso con evento y outbox en una operación, entonces todo queda; con un fallo inyectado en cualquiera de las escrituras, no queda nada.
  6. Dada una base vacía, cuando se ejecuta `task db:migrate` y luego `task test`, entonces todo pasa en local y en CI (criterio 6 de la definición).
  7. Dada la prueba de arquitectura, cuando corre, entonces sigue en verde, y una revisión con búsqueda del vocabulario prohibido sobre SQL y cadenas de las pruebas de M1 no encuentra términos de negocio.
  8. Dado `docs/02-arquitectura/kernel.md` (o el README de `kernel/`), cuando se lee, entonces dice que el proceso programador de timers no existe aún y que la vía de acceso privilegiado del staff es de B4.
- **Reglas de negocio:**
  - Las cinco leyes y su verificación: `docs/01-modelo/leyes.md`; criterios de éxito 1 a 8 de la definición.
  - Pruebas de leyes en `kernel/laws`, nunca omitidas (`AGENTS.md`, Testing).
  - Vocabulario neutro en pruebas: disciplina de revisión, porque `kernel/architecture_test.go` solo analiza identificadores Go.
  - Prueba de guardia contra omisiones: nueva.
- **Dependencias:** HU-kernel-migrations-01 a HU-kernel-idempotency-14.
- **Fuera de alcance:** batería completa de aislamiento, MFA de staff, observabilidad, respaldo y restauración (B6), contratos de B3.

---

## Verificación de Definition of Ready

| Historia | Estado | Rol | Escenario ejecutable | Sin TBD | Dependencias | Tamaño |
|---|---|---|---|---|---|---|
| HU-kernel-migrations-01 | Lista | sí | sí | sí | sí | M |
| HU-kernel-testinfra-02 | Lista | sí | sí | sí | sí | M |
| HU-kernel-spaces-03 | Lista | sí | sí | sí | sí | L |
| HU-kernel-participants-04 | Lista | sí | sí | sí | sí | M |
| HU-kernel-grants-05 | Lista | sí | sí | sí | sí | M |
| HU-kernel-permissions-06 | Lista (catálogo a aprobar en el PR) | sí | sí | sí | sí | M |
| HU-kernel-resources-07 | Lista | sí | sí | sí | sí | S |
| HU-kernel-exchanges-08 | Lista | sí | sí | sí | sí | L |
| HU-kernel-events-09 | Lista | sí | sí | sí | sí | M |
| HU-kernel-outbox-10 | Lista | sí | sí | sí | sí | M |
| HU-kernel-commitments-11 | Lista | sí | sí | sí | sí | L |
| HU-kernel-timers-12 | Lista | sí | sí | sí | sí | M |
| HU-kernel-execution-13 | Lista | sí | sí | sí | sí | L |
| HU-kernel-idempotency-14 | Lista | sí | sí | sí | sí | M |
| HU-kernel-laws-15 | Lista | sí | sí | sí | sí | M |
