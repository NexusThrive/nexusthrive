# Esquema `platform`

## Propósito

Guardar todo lo que NexusThrive necesita para operar como empresa por encima de todos los espacios: su equipo, lo que cobra, lo que publica, lo que aprueba y cómo da soporte.

## Alcance

Incluye equipo y permisos internos, planes y suscripciones, cobros, pruebas y códigos, distribuciones y pieles, solicitudes de capacidades, soporte, prospectos y banderas de funciones. Lo usa la [consola NexusThrive](../plataforma/consola.md). No guarda datos operativos de ningún negocio.

## Conceptos principales

- El equipo NexusThrive son participantes del **espacio `platform`** del kernel; `staff_members` agrega lo que solo aplica al equipo.
- Una **suscripción** une un espacio con un plan. Los **cobros** son lo que NexusThrive le cobra a ese espacio cada periodo.
- Una **piel** se publica como una **versión** (`skin_releases`). Un **despliegue** (`skin_rollouts`) la aplica a los espacios.
- Un **acceso de soporte** crea un `grant` temporal en el espacio del cliente, con motivo, y todo lo que se hace queda en `kernel.access_audit`.

## 1. Equipo NexusThrive

```mermaid
erDiagram
    STAFF_ROLES ||--o{ STAFF_MEMBERS : "asigna"
    STAFF_MEMBERS ||--o{ STAFF_INVITES : "invita"
    STAFF_ROLES ||--o{ STAFF_INVITES : "con rol"

    STAFF_ROLES {
        text key PK "founder, operations, support, sales, finance, engineering"
        text name
        text description
        text[] permissions
        bool system
    }
    STAFF_MEMBERS {
        uuid id PK
        uuid identity_id FK "kernel.identities"
        uuid participant_id FK "kernel.participants del espacio platform"
        text email
        text name
        text title
        text role_key FK
        text status "invited, active, disabled"
        uuid invited_by FK
        timestamptz joined_at
        timestamptz last_seen_at
    }
    STAFF_INVITES {
        uuid id PK
        text email
        text role_key FK
        uuid invited_by FK
        timestamptz expires_at
        timestamptz accepted_at
        timestamptz revoked_at
    }
```

## 2. Planes, suscripciones y cobros

```mermaid
erDiagram
    PLANS ||--o{ SUBSCRIPTIONS : "se contrata como"
    SUBSCRIPTIONS ||--o{ CHARGES : "genera"
    CHARGES ||--o{ CHARGE_EVIDENCE : "soporta"
    BENEFIT_CODES ||--o{ BENEFIT_REDEMPTIONS : "se canjea"
    BILLING_POLICY ||--o{ SUBSCRIPTIONS : "rige"

    PLANS {
        text key PK "essential, growth, signature"
        text name
        text tagline
        text currency
        numeric monthly_price
        numeric annual_price
        jsonb limits "users, participants, api_calls, ai_actions"
        text[] features
        bool is_active
        int sort_order
    }
    SUBSCRIPTIONS {
        uuid id PK
        uuid space_id FK "kernel.spaces"
        text plan_key FK
        text billing_cycle "monthly, annual"
        numeric price
        text currency
        text status "trialing, active, past_due, suspended, canceled"
        timestamptz trial_ends_at
        timestamptz current_period_start
        timestamptz current_period_end
        bool cancel_at_period_end
        timestamptz canceled_at
        text cancel_reason
    }
    CHARGES {
        uuid id PK
        uuid subscription_id FK
        uuid space_id FK
        timestamptz period_start
        timestamptz period_end
        numeric amount
        text currency
        text status "pending, paid, overdue, void, refunded"
        timestamptz due_at
        timestamptz paid_at
        text method "transfer, card, cash, gateway"
        text reference
        uuid recorded_by FK
        uuid confirmed_by FK
        timestamptz notice_sent_at
        text void_reason
    }
    CHARGE_EVIDENCE {
        uuid id PK
        uuid charge_id FK
        uuid file_id FK "kernel.files"
        text note
        uuid uploaded_by FK
    }
    BILLING_POLICY {
        int id PK "una sola fila"
        int default_trial_days
        int[] reminder_days "días antes del vencimiento"
        int grace_days
        int suspension_notice_days
        uuid updated_by FK
    }
    BENEFIT_CODES {
        uuid id PK
        text code UK
        text description
        int trial_days "días gratis personalizados"
        text plan_key FK
        int discount_pct
        int max_redemptions
        int redeemed_count
        timestamptz starts_at
        timestamptz expires_at
        bool is_active
    }
    BENEFIT_REDEMPTIONS {
        uuid id PK
        uuid code_id FK
        uuid space_id FK
        uuid redeemed_by FK
        timestamptz redeemed_at
    }
```

## 3. Distribuciones, pieles y solicitudes

```mermaid
erDiagram
    DISTRIBUTIONS ||--o{ SKIN_RELEASES : "publica"
    SKIN_RELEASES ||--o{ SKIN_ROLLOUTS : "se despliega en"
    SKIN_ROLLOUTS ||--o{ SKIN_ROLLOUT_ITEMS : "aplica a"
    SKIN_RELEASES ||--o{ SPACE_SKINS : "asignada a"
    CAPABILITY_REQUESTS }o--|| DISTRIBUTIONS : "de"

    DISTRIBUTIONS {
        text key PK "business"
        text name
        text status "active, preview, retired"
    }
    SKIN_RELEASES {
        uuid id PK
        text distribution_key FK
        text skin_key "gym, store, restaurant"
        int version
        jsonb manifest "vocabulario, roles, capacidades, preguntas, primeros pasos"
        text changelog
        text status "draft, published, retired"
        uuid published_by FK
        timestamptz published_at
    }
    SPACE_SKINS {
        uuid space_id PK
        uuid release_id FK
        timestamptz assigned_at
        uuid assigned_by FK
    }
    SKIN_ROLLOUTS {
        uuid id PK
        uuid release_id FK
        text strategy "selected, all"
        text status "previewed, running, done, failed"
        uuid started_by FK
        timestamptz started_at
        timestamptz finished_at
    }
    SKIN_ROLLOUT_ITEMS {
        uuid rollout_id FK
        uuid space_id FK
        text status "pending, applied, skipped, failed"
        jsonb diff "capacidades que se agregarían o quitarían"
        text error
    }
    CAPABILITY_REQUESTS {
        uuid id PK
        uuid space_id FK
        uuid requested_by FK "kernel.participants"
        text capability_key
        text reason
        jsonb ai_recommendation "decisión, confianza, razones"
        text status "pending, approved, rejected, applied"
        uuid decided_by FK
        text decision_note
        timestamptz decided_at
    }
```

## 4. Soporte, prospectos y banderas

```mermaid
erDiagram
    SUPPORT_TICKETS ||--o{ TICKET_MESSAGES : "conversa"
    SUPPORT_TICKETS ||--o{ SUPPORT_ACCESS_SESSIONS : "justifica"
    STAFF_MEMBERS ||--o{ SUPPORT_ACCESS_SESSIONS : "abre"
    LEADS ||--o{ LEAD_ACTIVITIES : "registra"
    FEATURE_FLAGS ||--o{ FEATURE_FLAG_OVERRIDES : "excepciones"

    SUPPORT_TICKETS {
        uuid id PK
        text number "2026-0001"
        uuid space_id FK
        text subject
        text description
        text category "billing, bug, how_to, data, access"
        text priority "low, normal, high, urgent"
        text status "open, waiting, resolved, closed"
        text channel "console, email, whatsapp"
        uuid requester_participant_id FK
        uuid assignee_staff_id FK
        timestamptz first_response_at
        timestamptz resolved_at
    }
    TICKET_MESSAGES {
        uuid id PK
        uuid ticket_id FK
        text author_kind "staff, customer, system"
        uuid author_id
        text body
        bool internal "nota interna, no la ve el cliente"
    }
    SUPPORT_ACCESS_SESSIONS {
        uuid id PK
        uuid staff_member_id FK
        uuid space_id FK
        uuid ticket_id FK
        text reason
        text scope "read, write"
        uuid grant_id FK "kernel.grants temporal"
        uuid approved_by FK "obligatorio si scope es write"
        timestamptz started_at
        timestamptz expires_at
        timestamptz ended_at
    }
    LEADS {
        uuid id PK
        text company
        text contact_name
        text phone
        text email
        text distribution_key
        text skin_key
        text city
        text source
        text stage "new, contacted, demo, trial, won, lost"
        uuid owner_staff_id FK
        text next_step
        timestamptz next_step_at
        numeric expected_value
        text lost_reason
        uuid converted_space_id FK
    }
    LEAD_ACTIVITIES {
        uuid id PK
        uuid lead_id FK
        text kind "call, message, meeting, note"
        text note
        uuid staff_id FK
        timestamptz at
    }
    FEATURE_FLAGS {
        text key PK
        text description
        bool default_enabled
    }
    FEATURE_FLAG_OVERRIDES {
        text flag_key FK
        uuid space_id FK
        bool enabled
    }
```

Además, `identity_limits` guarda cuántos espacios puede crear una identidad (por defecto, uno en autoservicio).

## Reglas e invariantes

- Un espacio de tipo distinto a `platform` tiene **exactamente una** suscripción vigente.
- Un cobro pagado **no se edita**: se anula con motivo y se crea otro.
- Una versión de piel publicada **no cambia**: se publica una versión nueva.
- Un despliegue de piel **solo toca** capacidades con `source = package`; nunca las que vienen de `ai`, `admin` o `code`.
- Un acceso de soporte con alcance `write` requiere aprobación de un segundo miembro del equipo y expira solo.
- Ninguna tabla de `platform` guarda clientes, ventas ni dinero de un negocio.

## Decisiones relacionadas

- [ADR-0011 · La plataforma por encima de los espacios](../../05-decisiones/0011-plataforma-por-encima.md)
- [ADR-0015 · Pieles versionadas con despliegue controlado](../../05-decisiones/0015-pieles-versionadas.md)
