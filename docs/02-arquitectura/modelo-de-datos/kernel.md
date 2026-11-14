# Esquema `kernel`

## Propósito

Guardar las siete piezas del modelo universal de forma que se cumplan las cinco leyes, sin conocer ningún mundo.

## Alcance

Incluye identidad y acceso, el libro de movimientos, los compromisos y el tiempo, la memoria y las señales, y el soporte operativo. No incluye nada del equipo NexusThrive (esquema `platform`) ni de ninguna distribución.

## Conceptos principales

| Pieza del modelo | Tablas |
|---|---|
| Participante | `identities`, `participants`, `contact_points` |
| Regla | `roles`, `grants`, `invitations`, `api_keys`, `space_capabilities` |
| Recurso | `resources`, `accounts` |
| Intercambio | `exchanges`, `entries` |
| Compromiso | `commitments`, `fulfillments` |
| Tiempo | `timers`, `scheduler_runs` |
| Evento | `events`, `outbox`, `webhook_endpoints`, `webhook_deliveries`, `notices` |

Un **espacio** (`space`) es la unidad de aislamiento: un negocio, una comunidad o una persona. El equipo NexusThrive vive en un espacio de tipo `platform`.

Una **identidad** es alguien que puede iniciar sesión (persona con Clerk, agente con llave). Un **participante** es esa identidad, o alguien sin cuenta, dentro de un espacio concreto. Una persona con tres negocios tiene una identidad y tres participantes.

## 1. Identidad y acceso

```mermaid
erDiagram
    SPACES ||--o{ SPACES : "agrupa (sedes)"
    SPACES ||--o{ PARTICIPANTS : "contiene"
    IDENTITIES ||--o{ PARTICIPANTS : "se presenta como"
    PARTICIPANTS ||--o{ CONTACT_POINTS : "se contacta por"
    SPACES ||--o{ ROLES : "define"
    ROLES ||--o{ GRANTS : "se otorga en"
    PARTICIPANTS ||--o{ GRANTS : "recibe"
    SPACES ||--o{ INVITATIONS : "emite"
    PARTICIPANTS ||--o{ API_KEYS : "agente dueño"
    SPACES ||--o{ SPACE_CAPABILITIES : "tiene activas"

    SPACES {
        uuid id PK
        text kind "platform, business, community, personal"
        text name
        text slug UK
        text status "provisioning, active, suspended, closed"
        uuid parent_space_id FK "sedes de un mismo grupo"
        text locale "es-CO por defecto"
        text timezone "America/Bogota por defecto"
        text currency "COP por defecto"
        timestamptz created_at
        timestamptz suspended_at
        text suspended_reason
        timestamptz closed_at
    }
    IDENTITIES {
        uuid id PK
        text provider "clerk, api_key, system"
        text subject "id externo, único por proveedor"
        text primary_email
        text display_name
        timestamptz last_seen_at
        timestamptz disabled_at
    }
    PARTICIPANTS {
        uuid id PK
        uuid space_id FK
        uuid identity_id FK "nulo si no tiene cuenta"
        text kind "person, organization, agent, system"
        text display_name
        text legal_name
        text document_type "CC, NIT, CE, PP"
        text document_number
        text country_code
        text status "active, archived"
        timestamptz archived_at
    }
    CONTACT_POINTS {
        uuid id PK
        uuid participant_id FK
        text channel "email, phone, whatsapp, push"
        text value
        text normalized_value
        bool is_primary
        timestamptz verified_at
        timestamptz consent_at "autorización de tratamiento de datos"
        text consent_source
        timestamptz opt_out_at
    }
    ROLES {
        uuid id PK
        uuid space_id FK
        text key "owner, admin, reception"
        text name
        text[] permissions
        bool system
    }
    GRANTS {
        uuid id PK
        uuid space_id FK
        uuid participant_id FK
        uuid role_id FK
        text[] extra_permissions
        uuid granted_by FK "participante otorgante, puede ser de otro espacio"
        uuid granted_by_space_id FK "espacio del otorgante"
        text granted_by_space_kind FK "kind de ese espacio, atado a spaces"
        text reason "obligatorio en accesos de soporte"
        timestamptz expires_at
        timestamptz created_at
        timestamptz revoked_at "lo fija el kernel con now()"
    }
    INVITATIONS {
        uuid id PK
        uuid space_id FK
        text email
        uuid role_id FK
        text token_hash
        timestamptz expires_at
        timestamptz accepted_at
        timestamptz revoked_at
    }
    API_KEYS {
        uuid id PK
        uuid space_id FK
        uuid participant_id FK "el agente o la integración"
        text name
        text key_prefix
        text key_hash
        text environment "live, test"
        text[] scopes
        text[] ip_allowlist
        timestamptz expires_at
        timestamptz last_used_at
        timestamptz revoked_at
    }
    SPACE_CAPABILITIES {
        uuid space_id PK "clave primaria (space_id, capability_key)"
        text capability_key PK "business.membership, business.pos"
        text status "active, suspended"
        text source "package, ai, admin, code"
        uuid enabled_by FK
        uuid enabled_by_space_id FK "espacio de quien la habilita"
        text enabled_by_space_kind FK "kind de ese espacio, atado a spaces"
        text reason
        timestamptz enabled_at
    }
```

## 2. Libro de movimientos

Todo valor que se mueve, sea dinero, unidades de inventario, tiempo o acceso, pasa por aquí. Una **cuenta** es un lugar donde un participante guarda un recurso; un **intercambio** mueve recursos entre cuentas con **asientos** que suman cero por recurso.

```mermaid
erDiagram
    SPACES ||--o{ RESOURCES : "define"
    RESOURCES ||--o{ ACCOUNTS : "se guarda en"
    PARTICIPANTS ||--o{ ACCOUNTS : "es titular de"
    ACCOUNTS ||--o{ ACCOUNTS : "subcuenta de"
    SPACES ||--o{ EXCHANGES : "registra"
    EXCHANGES ||--|{ ENTRIES : "se compone de"
    ACCOUNTS ||--o{ ENTRIES : "afecta"
    EXCHANGES ||--o| EXCHANGES : "revierte"
    ACCOUNTS ||--|| ACCOUNT_BALANCES : "proyecta"

    RESOURCES {
        uuid id PK
        uuid space_id FK
        text kind "money, unit, time, access"
        text code "COP, UND, MIN; mayúsculas"
        text name
        int scale "decimales permitidos, de 0 a 18"
    }
    ACCOUNTS {
        uuid id PK
        uuid space_id FK
        uuid holder_participant_id FK
        uuid resource_id FK
        uuid parent_account_id FK
        text class "asset, liability, equity, income, expense"
        text code
        text name
        text status "open, closed"
        timestamptz closed_at "coherente con status"
    }
    EXCHANGES {
        uuid id PK
        uuid space_id FK
        text kind "nombre con espacio de nombres: business.sale"
        text idempotency_key "obligatoria y única por espacio"
        uuid actor_participant_id FK "obligatorio"
        uuid reverses_exchange_id FK "intercambio compensatorio, uno por original"
        text memo
        jsonb metadata "objeto JSON, por defecto vacío"
        timestamptz occurred_at
        timestamptz recorded_at "lo fija el kernel"
    }
    ENTRIES {
        uuid id PK
        uuid exchange_id FK
        uuid space_id FK
        uuid account_id FK
        uuid resource_id FK
        numeric amount "positivo entra, negativo sale, nunca cero"
    }
    ACCOUNT_BALANCES {
        uuid account_id PK
        numeric amount
        bigint last_entry_seq
        timestamptz updated_at
    }
```

## 3. Compromisos y tiempo

```mermaid
erDiagram
    SPACES ||--o{ COMMITMENTS : "registra"
    PARTICIPANTS ||--o{ COMMITMENTS : "debe (deudor)"
    PARTICIPANTS ||--o{ COMMITMENTS : "recibe (acreedor)"
    RESOURCES ||--o{ COMMITMENTS : "en"
    COMMITMENTS ||--o{ FULFILLMENTS : "se cumple con"
    ENTRIES ||--o{ FULFILLMENTS : "aporta"
    COMMITMENTS ||--o| TIMERS : "vence con"

    COMMITMENTS {
        uuid id PK
        uuid space_id FK
        text kind "business.debt, business.membership"
        uuid debtor_participant_id FK
        uuid creditor_participant_id FK
        uuid resource_id FK
        numeric amount
        numeric fulfilled_amount "derivado de fulfillments"
        timestamptz starts_at
        timestamptz due_at "obligatorio; solo se reprograma hacia el futuro"
        text status "open, fulfilled, canceled, expired; derivado"
        text origin_ref "referencia a la distribución: business.sale/uuid"
        text canceled_reason
        timestamptz closed_at "lo fija el kernel"
        timestamptz created_at
        uuid created_by_participant_id FK
    }
    FULFILLMENTS {
        uuid id PK
        uuid space_id FK
        uuid commitment_id FK
        uuid entry_id FK "único: un asiento se aplica una vez"
        uuid resource_id FK
        numeric amount "igual al del asiento"
        timestamptz created_at
    }
    TIMERS {
        uuid id PK "igual al id del compromiso"
        uuid space_id FK
        uuid commitment_id FK
        timestamptz fire_at "igual a due_at mientras está scheduled"
        text status "scheduled, fired, canceled; derivado"
        timestamptz fired_at "lo fija el kernel"
        timestamptz created_at "lo fija el kernel"
    }
    SCHEDULER_RUNS {
        uuid id PK
        text task "timers, outbox, webhooks"
        timestamptz started_at
        timestamptz finished_at
        text status "ok, failed"
        int processed
        text error
    }
```

## 4. Memoria y señales

```mermaid
erDiagram
    SPACES ||--o{ EVENTS : "recuerda"
    EVENTS ||--o{ OUTBOX : "se publica a"
    SPACES ||--o{ WEBHOOK_ENDPOINTS : "configura"
    WEBHOOK_ENDPOINTS ||--o{ WEBHOOK_DELIVERIES : "recibe"
    EVENTS ||--o{ WEBHOOK_DELIVERIES : "entrega"
    EVENTS ||--o{ NOTICES : "origina"
    PARTICIPANTS ||--o{ NOTICES : "destinatario"
    CONTACT_POINTS ||--o{ NOTICES : "por"

    EVENTS {
        bigint seq PK
        uuid id UK
        uuid space_id FK
        text type "kernel.space.created, sin sufijo de versión"
        int version
        text subject_ref
        jsonb payload "objeto JSON; esquema en B3"
        uuid actor_participant_id FK
        text actor_kind "person, agent, api_key, system"
        text request_id
        timestamptz occurred_at
    }
    OUTBOX {
        bigint event_seq PK "con subscriber; FK compuesta (event_seq, space_id)"
        text subscriber PK "webhooks, n8n, ai, internal"
        uuid space_id FK
        text status "pending, sent, failed, dead"
        int attempts "0 a 5"
        timestamptz next_attempt_at
        text last_error
        timestamptz delivered_at
        uuid claim_token "reclamo vigente"
        timestamptz claimed_until "fin del arrendamiento"
        timestamptz created_at
    }
    WEBHOOK_ENDPOINTS {
        uuid id PK
        uuid space_id FK
        text url
        text secret_hash
        text[] event_types
        text status "active, disabled"
        int consecutive_failures
    }
    WEBHOOK_DELIVERIES {
        uuid id PK
        uuid endpoint_id FK
        bigint event_seq FK
        text status
        int attempts
        int response_status
        text last_error
        timestamptz delivered_at
    }
    NOTICES {
        uuid id PK
        uuid space_id FK
        uuid recipient_participant_id FK
        uuid contact_point_id FK
        bigint event_seq FK
        text channel "whatsapp, email, sms, push"
        text template_key
        text status "queued, sent, failed, skipped"
        text skip_reason "sin consentimiento, opt-out"
        timestamptz sent_at
    }
```

## 5. Soporte operativo

| Tabla | Para qué |
|---|---|
| `idempotency_records` | Guarda la respuesta de cada operación por `space_id` + clave durante 24 horas; una repetición devuelve la misma respuesta |
| `usage_counters` | Consumo por espacio, medidor y periodo (usuarios, participantes, llamadas a la API, acciones de IA) contra el límite del plan |
| `access_audit` | Toda lectura o escritura hecha por un actor privilegiado (soporte, agentes) con motivo y `request_id`. Columnas: `id`, `space_id` (espacio auditado), `actor_participant_id` y `actor_space_id` y `actor_space_kind` (el actor vive en su propio espacio: `platform` o el espacio auditado; el `kind` se ata a `spaces (id, kind)` y un `CHECK` impone el alcance), `action` (`read`, `write`), `subject_ref` opcional, `reason` y `request_id` obligatorios, `grant_id` opcional y `occurred_at`. Es de solo agregar |
| `files` | Archivos (logos, comprobantes, documentos) con `sha256`, tipo y tamaño; el contenido vive en almacenamiento de objetos |

## Reglas e invariantes

- **Conservación:** disparadores de restricción diferidos (`exchanges_balance_check` sobre `exchanges` y `entries_balance_check` sobre `entries`, ambos con `kernel.check_exchange_balance()`) verifican al confirmar la transacción que el intercambio tenga al menos dos asientos y que `sum(entries.amount)` por `(exchange_id, resource_id)` sea cero. El disparador de `exchanges` es el que rechaza el intercambio vacío. Los errores salen con `integrity_constraint_violation` y el nombre de la regla en `CONSTRAINT` (`exchanges_min_entries`, `exchanges_conservation`).
- **Memoria:** `events`, `entries`, `exchanges` y `fulfillments` no admiten `UPDATE` ni `DELETE`; un error se corrige con un intercambio cuyo `reverses_exchange_id` apunta al original.
- **Memoria y outbox:** la ley de memoria cubre eventos y asientos; no cubre el outbox, que es estado de trabajo y cambia por diseño. Su cambio está acotado por un disparador que rige para todos los roles (ver Outbox) y sus filas nunca se borran.
- **Eventos:** `type` son dos o más segmentos en minúsculas separados por punto (`kernel.space.created`) y no lleva sufijo de versión, porque la versión es la columna `version`. `payload` es un objeto JSON; su validación con JSON Schema llega en B3. `seq` es `GENERATED ALWAYS`: único, creciente por orden de asignación, con huecos tras un rollback, y no es el orden de confirmación (ADR-0028). Una lectura "desde `seq = n`" es inclusiva y devuelve solo eventos confirmados del espacio actual en orden ascendente; el consumidor guarda como cursor el último `seq` más uno, reanuda con traslape y deduplica por `id`. Un evento sin actor se rechaza, salvo `actor_kind = system`; el actor, si es un participante, debe pertenecer al mismo espacio (clave foránea compuesta `(actor_participant_id, space_id)` hacia `participants (id, space_id)`).
- **Inmutabilidad de eventos:** dos capas. El rol de la API tiene `SELECT` sobre la tabla e `INSERT` solo por columnas (todas menos `seq`, así que tampoco puede fijarlo con `OVERRIDING SYSTEM VALUE` ni con `COPY`), y la RLS solo tiene políticas `SELECT` e `INSERT`. Además, un disparador por fila (`UPDATE`, `DELETE`) y otro por sentencia (`TRUNCATE`) llaman a `kernel.reject_event_change()`, que falla con `restrict_violation` incluso para el dueño y el administrador, y aunque un permiso se conceda por error. Un superusuario puede deshabilitar el disparador con DDL deliberado (ADR-0028).
- **Outbox** (ADR-0031): una fila por evento y suscriptor, con clave `(event_seq, subscriber)`, `space_id` y clave foránea compuesta `(event_seq, space_id)` hacia `events` (que por eso tiene `UNIQUE (seq, space_id)`). `subscriber` es uno de `webhooks`, `n8n`, `ai`, `internal`. La restricción `outbox_state_check` liga el estado a los intentos: `pending` con 0 intentos; `failed` con 1 a 4 y error; `sent` con `delivered_at` y sin reclamo; `dead` exactamente con 5 intentos y error. El límite de 5 vive ahí y en `model.OutboxMaxAttempts`: la fila pasa a `dead` en el quinto fallo. **Reclamo con arrendamiento:** `claim_token` y `claimed_until` (5 minutos) sobreviven a la transacción del reclamo, porque `FOR UPDATE SKIP LOCKED` solo protege mientras la transacción vive; ambos son nulos o ambos presentes. Un reclamo vencido cuenta como intento fallido (`last_error = 'claim expired'`), con la misma espera y el mismo paso a `dead`. La entrega es al menos una vez: el consumidor deduplica por `event.id`. Una transacción reclama un solo espacio. Espera tras el intento 1 a 4: 30 s, 2 min, 10 min y 1 h (valores de partida que B3 puede cambiar con una migración). **Dos capas:** el rol de la API tiene `SELECT`, `INSERT (event_seq, space_id, subscriber, next_attempt_at)` y `UPDATE` solo sobre `status`, `attempts`, `next_attempt_at`, `last_error`, `delivered_at`, `claim_token` y `claimed_until`, sin `DELETE`, `TRUNCATE`, `REFERENCES` ni `TRIGGER`; la RLS tiene políticas `SELECT`, `INSERT` y `UPDATE`, y ninguna de `DELETE`. `kernel.guard_outbox_change()` (`restrict_violation`, también para el dueño) fija: la identidad (`event_seq`, `space_id`, `subscriber`, `created_at`) no cambia; `sent` y `dead` son finales; una fila libre solo puede reclamarse; una reclamada suelta el reclamo y solo sale como `sent` (sin tocar intentos, error ni calendario) o como `failed` o `dead` (exactamente un intento más y `next_attempt_at` sin retroceder); volver a reclamarla es un error. `kernel.reject_outbox_removal()` rechaza `DELETE` y `TRUNCATE`, también en cascada. Ninguna función es `SECURITY DEFINER` y ambas fijan `search_path`. Límites: el disparador valida la forma y no el tiempo (no ve el reloj inyectable: `delivered_at` y `next_attempt_at` los fija el kernel en Go), un superusuario puede deshabilitarlo con DDL deliberado y el proceso del kernel que reclama podría forjar `delivered_at`; nada reabre una fila `dead` hasta la consola de B4. El permiso `kernel.outbox.claim` lo aplica HU-kernel-execution-13.
- **Recursos y cuentas:** `resources.code` va en mayúsculas (`^[A-Z][A-Z0-9_]{0,31}$`), es único por `(space_id, code)` y `scale` es un entero de 0 a 18. El recurso es inmutable para la API (sin `UPDATE`) y, para cualquiera, `scale` y `kind` quedan congelados cuando existe un asiento del recurso: `kernel.guard_resource_change()` (`restrict_violation`) exige que el cambio corra en el espacio del recurso, toma `LOCK TABLE kernel.entries IN SHARE MODE` para esperar a los insertores en vuelo y rechaza si hay asientos; desde la migración 00012 hace lo mismo con `kernel.commitments` y rechaza si hay compromisos del recurso. `accounts.name` y `accounts.code` son obligatorios y el código es único por `(space_id, holder_participant_id, code)`. `closed_at` es obligatorio si y solo si `status = closed`. Toda referencia a datos de espacio es una clave foránea compuesta con `space_id`, porque la comprobación de claves ignora la RLS: `(holder_participant_id, space_id)` hacia `participants`, `(resource_id, space_id)` hacia `resources` y `(parent_account_id, space_id, resource_id)` hacia `accounts`, de modo que la cuenta madre comparte espacio y recurso. Una cuenta nace abierta (la API no tiene `INSERT` sobre `status` ni `closed_at`), pasa una sola vez de `open` a `closed` y no se reabre: un disparador por fila (`kernel.reject_account_reopen()`, `restrict_violation`) lo impide incluso al dueño, y ese mismo disparador fija `closed_at := now()` al cerrar, de modo que la API no puede falsificar la fecha. Los asientos referencian `accounts (id, space_id, resource_id)` y un disparador de `entries` rechaza las cuentas cerradas bajo `FOR SHARE` (ver Libro de movimientos).
- **Libro de movimientos** (ADR-0030): `exchanges.idempotency_key` y `actor_participant_id` son obligatorios; la clave es única por `(space_id, idempotency_key)` (hoy una clave repetida se rechaza; HU-kernel-idempotency-14 devolverá el resultado original). `kind` sigue el formato de los tipos de evento y `metadata` es un objeto JSON, por defecto `{}`. `entries.amount` es `numeric` distinto de cero y de valor absoluto menor que 1e30; `NaN` e infinitos se rechazan. Las claves foráneas son compuestas con `space_id`: `(exchange_id, space_id)`, `(account_id, space_id, resource_id)`, `(actor_participant_id, space_id)` y `(reverses_exchange_id, space_id)`. `entries` no lleva `commitment_id`: el vínculo con un compromiso vive solo en `fulfillments` (ADR-0033) y `entries` añade `UNIQUE (id, space_id, resource_id)` como destino de esa clave. El rol de la API tiene `SELECT` e `INSERT` por columnas (`recorded_at` queda fuera) y ninguna política de `UPDATE` ni `DELETE`; además, `kernel.reject_ledger_change()` rechaza `UPDATE`, `DELETE` y `TRUNCATE` (también en cascada) con `restrict_violation`, incluso al dueño, y `kernel.stamp_exchange()` fija `recorded_at` con `now()`. `kernel.guard_entry_insert()` exige, en este orden: el intercambio se creó en la misma transacción (`entries_same_transaction`, así no se añaden asientos a un intercambio confirmado), la cuenta existe en el espacio, está abierta (lectura con `FOR SHARE`, que se serializa con el cierre) y tiene el recurso del asiento (`entries_account_open`, `entries_account_resource`), y el monto cabe en la escala del recurso por valor (`entries_amount_scale`); el monto se guarda canónico. Un intercambio se compensa una sola vez (`UNIQUE (space_id, reverses_exchange_id)`) y el compensatorio suma, por cuenta, lo opuesto exacto del original (`exchanges_reversal_mirror`); un intercambio tiene de 2 a 100 asientos (`exchanges_min_entries` y `exchanges_max_entries`; el conteo de la comprobación se detiene en 101 para acotar su coste). Ninguna función es `SECURITY DEFINER` y todas fijan `search_path`. Un superusuario puede deshabilitar los disparadores con DDL deliberado (ADR-0028).
- **Roles, grants y capacidades:** `roles.key` cumple `^[a-z][a-z0-9_]{0,63}$` y es único por `(space_id, key)`. Toda lista de permisos (`roles.permissions`, `grants.extra_permissions`) no tiene elementos nulos, tiene a lo sumo 200 y cada elemento son dos o más segmentos en minúsculas separados por punto (`kernel.valid_permission_list`). Las referencias son claves foráneas compuestas con `space_id`: `(participant_id, space_id)` hacia `participants` y `(role_id, space_id)` hacia `roles`. Un grant activo por `(space_id, participant_id, role_id)` (índice único parcial donde `revoked_at` es nulo). El otorgante puede vivir en otro espacio (acceso de soporte), por eso lleva `granted_by`, `granted_by_space_id` y `granted_by_space_kind`, los tres nulos o los tres presentes, con las claves `(granted_by, granted_by_space_id)` hacia `participants` y `(granted_by_space_id, granted_by_space_kind)` hacia `spaces (id, kind)` (que por eso tiene `UNIQUE (id, kind)`); el cliente no puede declarar un `kind` falso. Solo otorga el propio espacio o un espacio `platform`, nadie se otorga a sí mismo, y un otorgante de un espacio `platform` distinto del espacio del grant (acceso de soporte) exige `reason` y `expires_at` (`CHECK` de la tabla; el alta de staff dentro del propio espacio `platform` queda libre). El kernel no crea grants ya vencidos; la vigencia (no revocado y `expires_at` nulo o futuro) se evalúa en Go con un reloj inyectable. `space_capabilities` usa la clave `(space_id, capability_key)`, nace `active` (la API no inserta `status`) y aplica las mismas reglas de alcance a `enabled_by*`; con `source = admin` exige `enabled_by`. Reglas de escalada de privilegios: ADR-0029.
- **Inmutabilidad del `kind` de un espacio:** el rol de la API tiene `UPDATE` en `spaces` solo sobre `name`, `status`, `suspended_at`, `suspended_reason` y `closed_at`, y el disparador `kernel.reject_space_kind_change()` rechaza (`restrict_violation`) todo cambio de `kind`, también del dueño. El `INSERT` del rol de la API es por columnas (`id`, `kind`, `name`, `slug`, `parent_space_id`, `locale`, `timezone`, `currency`) y `kernel.reject_api_platform_space()` rechaza la creación de espacios `platform` cuando `current_user` es el rol de la API; esos espacios los aprovisiona el administrador al arrancar (ADR-0029).
- **Revocación:** `revoked_at` no es falsificable: un disparador por fila (`kernel.guard_grant_update()`) fija `revoked_at := now()`, rechaza con `restrict_violation` cualquier cambio de otra columna y cualquier cambio de un grant ya revocado (nadie lo des-revoca, ni el dueño). La API solo tiene `UPDATE (revoked_at)`. Revocar dos veces devuelve `already_done`. Los grants no se borran (disparadores `BEFORE DELETE` y `BEFORE TRUNCATE`).
- **Permisos solo de plataforma y espacios cerrados** (ADR-0032, migración 00011): `kernel.platform_only_permissions()` lista las claves que solo un espacio `platform` puede tener (hoy `kernel.capability.write`) y el disparador `kernel.guard_platform_only_permissions()` rechaza con `restrict_violation`, en `INSERT` y en `UPDATE` de la lista, un rol (`roles.permissions`) o un grant (`grants.extra_permissions`) de otro espacio que la incluya; falla cerrado (un espacio ilegible no es `platform`) y vale para todos, también para el dueño. `kernel.guard_space_closed()` hace terminal el estado `closed`: nadie reabre el espacio ni borra o cambia `closed_at`, y al cerrar fija `closed_at := now()` (no falsificable); un espacio que no está cerrado no lleva `closed_at`. Una suspensión sí se revierte con el `UPDATE (status)` del rol de la API hasta que B3 y B4 certifiquen al actor. Las tres funciones fijan `search_path = pg_catalog, pg_temp` y ninguna es `SECURITY DEFINER`. El catálogo de claves (`model.KernelCatalog()`) vive en Go: SQL solo conoce el formato y la lista de plataforma, y una prueba ata ambas.
- **Rol `owner`:** `state.CreateStandardRoles` crea, con el actor `system`, el rol `owner` (`system = true`) con diez claves y nunca `kernel.capability.write`, `kernel.timer.process` ni `kernel.outbox.claim`. Ningún código lo trata como autoridad: es solo una lista. Repetirlo es `rule_violation`.
- **Auditoría de acceso:** `access_audit` es de solo agregar en dos capas, como `events`: la API solo tiene `SELECT` e `INSERT` por columnas (sin `occurred_at`) y los disparadores de `kernel.reject_access_audit_change()` fallan con `restrict_violation` incluso para el dueño. Un superusuario puede deshabilitarlos con DDL deliberado.
- **Compromiso** (ADR-0033, migración 00012): `due_at` y `status` son obligatorios y no tienen valor por defecto; `fulfilled_amount` nunca supera `amount` (`CHECK`). El kernel deriva el estado, `fulfilled_amount` y `closed_at`: el rol de la API inserta solo con `status = open` (`kernel.guard_commitment_insert()` rechaza otro valor y cualquier `fulfilled_amount`, `closed_at` o `canceled_reason` falsos con `restrict_violation`, también al dueño, y fija `created_at`) y no tiene `UPDATE` sobre `status`, `closed_at` ni la identidad. `kernel.guard_commitment_update()` rechaza todo cambio de un compromiso cerrado (`fulfilled`, `canceled`, `expired`) y admite un solo efecto por sentencia sobre uno abierto: `fulfilled_amount` igual a la suma de `fulfillments` (si alcanza `amount` pasa a `fulfilled`), `canceled_reason` de nulo a un motivo (pasa a `canceled`) `due_at` hacia el futuro o `status = expired` solo por la cascada de un timer (ver Timers); fija `closed_at := now()`. `due_at` se reprograma desde timers-12 (ver Timers). El rol de la API tiene `UPDATE (fulfilled_amount, canceled_reason, due_at, status)` porque los disparadores corren con sus derechos, y la comprobación diferida `commitments_fulfilled_sum` verifica al confirmar que `fulfilled_amount` es la suma (falla cerrado si la transacción cambió de espacio). La escala del monto sigue la del recurso (`commitments_amount_scale`), el deudor es distinto del acreedor y las claves foráneas compuestas son `(debtor_participant_id, space_id)`, `(creditor_participant_id, space_id)`, `(created_by_participant_id, space_id)` y `(resource_id, space_id)`. `scale` y `kind` de un recurso con compromisos no cambian.
- **Cumplimiento** (ADR-0033): `fulfillments` es de solo agregar (`kernel.reject_ledger_change()`, también `TRUNCATE` en cascada) y los compromisos no se borran (`kernel.reject_commitment_removal()`). `kernel.guard_fulfillment_insert()` toma `SELECT ... FOR NO KEY UPDATE` sobre el compromiso, de modo que dos cumplimientos, o un cumplimiento y una cancelación, se serializan y el segundo relee lo confirmado; luego exige compromiso abierto (`fulfillments_commitment_open`), asiento del mismo espacio y recurso, positivo, de una cuenta del acreedor (`fulfillments_entry_creditor`), fuera de un intercambio de compensación (`fulfillments_entry_reversal`), por el monto completo del asiento (`fulfillments_amount_entry`) y dentro de lo pendiente (`fulfillments_within_pending`). Un asiento se aplica una vez (`UNIQUE (entry_id)`). Las claves `(commitment_id, space_id, resource_id)` y `(entry_id, space_id, resource_id)` impiden mezclar espacios o recursos aunque falte el disparador.
- **Timers** (ADR-0034, migración 00013): cada compromiso tiene un timer de vencimiento con su mismo `id` (`kernel.timers`, con RLS forzada, `UNIQUE` parcial `timers_one_scheduled_key` y clave foránea compuesta `(commitment_id, space_id)`). Lo crea el disparador `commitments_timer_create` y lo mueven y cancelan `commitments_timer_sync`, para todo escritor. Un compromiso llega a `expired` solo por cascada: `kernel.timers_guard_update()` deja pasar `scheduled` a `fired` únicamente si el compromiso está abierto y `fire_at <= now()` (reloj de la base) y fija `fired_at`; `timers_expire_commitment` expira el compromiso y `guard_commitment_update()` lo acepta solo con `due_at <= now()` y un timer del compromiso disparado en esta misma transacción. Un timer terminal no cambia, no se borra ni se trunca (`kernel.reject_timer_removal()`, también en cascada) y `fired_at` y `created_at` no están en ningún grant. Reprogramar (`due_at`) exige un instante estrictamente futuro (`commitments_reschedule_past`). La restricción diferida `timers_coherent` comprueba al confirmar, para todo escritor, que un compromiso abierto tiene exactamente un timer `scheduled` en `due_at`, que uno cerrado no tiene ninguno y que un timer `fired` va con un compromiso `expired` y un evento `kernel.commitment.expired` v1 del espacio con `subject_ref = 'kernel.commitment/<id>'` (falla cerrado si la transacción cambió de espacio). No existen `signal_type` ni `payload`: los recordatorios previos al vencimiento quedan fuera de alcance.
- **Permiso:** toda fila escrita tiene un actor (`actor_participant_id` o equivalente); no existen escrituras anónimas.
- **Movimiento:** al crear o reprogramar un compromiso con vencimiento se crea o mueve su `timer` en la misma transacción (disparador de la base), y al cerrarlo se cancela. Al vencer, `state.FireDueTimers` dispara el timer, expira el compromiso y escribe `kernel.commitment.expired` y su fila de outbox `internal` en una sola transacción.
- **Aislamiento:** RLS por `space_id` en todas las tablas con datos de espacio, con `ENABLE` y `FORCE`. La política compara con `kernel.current_space_id()`, que lee el parámetro `kernel.space_id` fijado por transacción (`set_config(..., true)`, equivalente a `SET LOCAL`); sin valor no hay filas ni error. En `spaces` la política compara `id`. La API se conecta como `nexusthrive_api`, un rol sin superusuario, sin `BYPASSRLS` y sin ser dueño de objetos (ADR-0027). La función fija su `search_path` (`pg_catalog, pg_temp`) y una migración rechaza el rol si tiene `SUPERUSER`, `BYPASSRLS`, `CREATEROLE`, `CREATEDB` o `REPLICATION`. `identities` no tiene `space_id`: una política `SELECT` la muestra solo si existe un participante suyo en el espacio actual, y una política `INSERT` permite crearla antes de que tenga participante; la API no puede actualizarla ni borrarla. En `participants`, `UPDATE` se limita a `status` y `archived_at`, y un índice único parcial `(space_id, identity_id)` donde `identity_id` no es nulo garantiza un participante por identidad en cada espacio.

## Ejemplo

Un socio paga 50.000 COP en efectivo de un plan de 100.000; queda debiendo 50.000:

| Tabla | Filas |
|---|---|
| `commitments` | Deuda de 100.000 COP, deudor el socio, acreedor el negocio, vence en 5 días |
| `exchanges` | `business.payment`, idempotente por la clave de la recepción |
| `entries` | +50.000 en la cuenta caja del negocio · −50.000 en la cuenta por cobrar del socio |
| `fulfillments` | 50.000 aplicados a la deuda (`fulfilled_amount` = 50.000) |
| `timers` | Timer de vencimiento en 5 días; al dispararse, la deuda pasa a `expired` |
| `events` + `outbox` | `business.payment.received` (versión 1) pendiente para n8n |

## Decisiones relacionadas

- [ADR-0001 · El kernel no conoce ningún mundo](../../05-decisiones/0001-kernel-sin-mundo.md)
- [ADR-0009 · Libro de movimientos de doble partida](../../05-decisiones/0009-libro-de-movimientos.md)
- [ADR-0029 · Otorgante entre espacios y reglas contra la escalada de privilegios](../../05-decisiones/0029-otorgante-entre-espacios-y-escalada.md)
- [ADR-0031 · Outbox: reclamo de un solo espacio, arrendamiento, entrega al menos una vez y valores de partida](../../05-decisiones/0031-outbox-reclamo-y-arrendamiento.md)
- [ADR-0032 · Catálogo de permisos y verificación](../../05-decisiones/0032-catalogo-de-permisos-y-verificacion.md)
