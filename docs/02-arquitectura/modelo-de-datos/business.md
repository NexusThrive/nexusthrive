# Esquema `business`

## Propósito

Guardar el idioma de los negocios (clientes, catálogo, ventas, cartera, caja, documentos y patrones) apuntando siempre a las piezas del kernel.

## Alcance

Corresponde a la **etapa 2**: se construye después de terminar el kernel y la plataforma. Incluye la base de Business y el patrón Membresía. Los patrones Productos y Preparación se agregan después.

## Conceptos principales

Cada tabla de Business tiene una columna que la conecta con el kernel. El dinero, las deudas y las existencias nunca se guardan aquí: se leen del libro de movimientos.

| Business | Kernel |
|---|---|
| `businesses` | `spaces` de tipo `business` |
| `customers`, empleados | `participants` |
| `items` (producto, servicio, plan) | `resources` |
| `payment_methods`, `cash_boxes` | `accounts` |
| Venta pagada, pago, gasto | `exchanges` + `entries` |
| Venta a crédito, deuda | `commitments` de dinero |
| `memberships` | `commitments` de acceso |

## 1. Clientes, catálogo y ventas

```mermaid
erDiagram
    BUSINESSES ||--o{ CUSTOMERS : "atiende"
    CUSTOMERS ||--o{ CUSTOMER_NOTES : "historial"
    BUSINESSES ||--o{ ITEM_CATEGORIES : "organiza"
    ITEM_CATEGORIES ||--o{ ITEMS : "agrupa"
    TAX_RATES ||--o{ ITEMS : "grava"
    CUSTOMERS ||--o{ SALES : "compra"
    SALES ||--|{ SALE_LINES : "contiene"
    ITEMS ||--o{ SALE_LINES : "vendido en"
    SALES ||--o{ DOCUMENTS : "respalda"
    NUMBERING_SEQUENCES ||--o{ DOCUMENTS : "numera"

    BUSINESSES {
        uuid space_id PK "kernel.spaces"
        text legal_name
        text trade_name
        text tax_id "NIT"
        text tax_id_check_digit
        text tax_regime
        text city
        text address
        text whatsapp_number
        uuid logo_file_id FK
        text onboarding_state "creating, preparing, first_steps, operating"
        jsonb onboarding_answers
        jsonb settings
    }
    CUSTOMERS {
        uuid id PK
        uuid space_id FK
        uuid participant_id FK "kernel.participants"
        text name
        text document_type
        text document_number
        date birth_date
        text city
        jsonb profile "campos de la piel"
        text[] tags
        text stage "lead, active, recurring, inactive"
        text source
        uuid owner_participant_id FK
        timestamptz archived_at
    }
    CUSTOMER_NOTES {
        uuid id PK
        uuid customer_id FK
        uuid author_participant_id FK
        text body
    }
    ITEM_CATEGORIES {
        uuid id PK
        uuid space_id FK
        uuid parent_id FK
        text name
        int sort_order
    }
    ITEMS {
        uuid id PK
        uuid space_id FK
        uuid resource_id FK "kernel.resources"
        uuid category_id FK
        text kind "product, service, plan, ticket"
        text name
        text description
        text sku
        text unit_code
        numeric price
        numeric cost
        text tax_code FK
        bool track_stock
        bool sellable
        bool is_active
        uuid image_file_id FK
    }
    TAX_RATES {
        text code PK "IVA19, IVA5, INC8, EXENTO"
        uuid space_id FK
        text kind "vat, consumption, withholding"
        numeric rate_pct
        date valid_from
        date valid_to
    }
    SALES {
        uuid id PK
        uuid space_id FK
        int number
        uuid customer_id FK
        text channel "counter, pos, link, membership"
        text status "paid, partially_paid, unpaid, voided"
        numeric subtotal
        numeric discount_total
        numeric tax_total
        numeric total
        uuid exchange_id FK "kernel.exchanges del pago inicial"
        uuid commitment_id FK "kernel.commitments si queda deuda"
        uuid cash_session_id FK
        uuid created_by FK
        text void_reason
        timestamptz voided_at
    }
    SALE_LINES {
        uuid id PK
        uuid sale_id FK
        uuid item_id FK
        text description
        numeric quantity
        numeric unit_price
        numeric discount_pct
        numeric tax_rate_pct
        numeric total
    }
    DOCUMENTS {
        uuid id PK
        uuid space_id FK
        text kind "receipt, invoice, credit_note"
        text prefix
        int number
        uuid sale_id FK
        uuid payment_id FK
        text customer_name "copia al emitir"
        text customer_document
        numeric subtotal
        numeric tax_total
        numeric total
        text status "issued, voided"
        text fiscal_status "not_applicable, queued, accepted, rejected"
        uuid file_id FK
        timestamptz issued_at
    }
    NUMBERING_SEQUENCES {
        uuid id PK
        uuid space_id FK
        text document_kind
        text prefix
        text resolution_number
        int range_from
        int range_to
        int next_number
        date valid_from
        date valid_to
    }
```

## 2. Cobros y caja

```mermaid
erDiagram
    BUSINESSES ||--o{ PAYMENT_METHODS : "acepta"
    PAYMENT_METHODS ||--o{ PAYMENTS : "por"
    CUSTOMERS ||--o{ PAYMENTS : "paga"
    BUSINESSES ||--o{ PAYMENT_LINKS : "envía"
    PAYMENT_LINKS ||--o| PAYMENTS : "se convierte en"
    BUSINESSES ||--o{ PROVIDER_EVENTS : "recibe"
    BUSINESSES ||--o{ CASH_BOXES : "tiene"
    CASH_BOXES ||--o{ CASH_SESSIONS : "abre"
    CASH_SESSIONS ||--o{ PAYMENTS : "recibe en efectivo"
    CASH_SESSIONS ||--o{ EXPENSES : "paga"

    PAYMENT_METHODS {
        text key PK "cash, transfer, nequi, card, wompi"
        uuid space_id FK
        text label
        uuid account_id FK "kernel.accounts donde entra el dinero"
        bool is_active
        int sort_order
    }
    PAYMENTS {
        uuid id PK
        uuid space_id FK
        uuid customer_id FK
        uuid exchange_id FK "kernel.exchanges"
        text method_key FK
        numeric amount
        text reference
        text provider_ref
        uuid cash_session_id FK
        uuid received_by FK
        text void_reason
        timestamptz voided_at
    }
    PAYMENT_LINKS {
        uuid id PK
        uuid space_id FK
        text reference UK
        uuid commitment_id FK "kernel.commitments que salda"
        numeric amount
        text provider "wompi"
        text url
        text status "pending, paid, expired, canceled"
        timestamptz expires_at
        timestamptz paid_at
    }
    PROVIDER_EVENTS {
        uuid id PK
        text provider
        text external_id UK "evita procesar dos veces"
        jsonb payload
        timestamptz processed_at
        text error
    }
    CASH_BOXES {
        uuid id PK
        uuid space_id FK
        uuid account_id FK "kernel.accounts"
        text name
        numeric fund_amount
        uuid responsible_participant_id FK
        bool is_active
    }
    CASH_SESSIONS {
        uuid id PK
        uuid box_id FK
        uuid opened_by FK
        numeric opening_amount
        timestamptz opened_at
        uuid closed_by FK
        numeric expected_amount "calculado del libro"
        numeric counted_amount
        numeric difference
        text closing_notes
        timestamptz closed_at
    }
    EXPENSES {
        uuid id PK
        uuid space_id FK
        uuid exchange_id FK "kernel.exchanges"
        text category
        text description
        numeric amount
        text supplier_name
        uuid cash_session_id FK
        date expense_date
        text void_reason
        timestamptz voided_at
    }
```

## 3. Patrón Membresía

```mermaid
erDiagram
    ITEMS ||--o| PLANS : "es un"
    PLANS ||--o{ MEMBERSHIPS : "se asigna como"
    CUSTOMERS ||--o{ MEMBERSHIPS : "tiene"
    MEMBERSHIPS ||--o{ MEMBERSHIP_FREEZES : "se congela"
    CUSTOMERS ||--o{ ACCESS_CREDENTIALS : "usa"
    CUSTOMERS ||--o{ CHECKINS : "entra"
    MEMBERSHIPS ||--o{ CHECKINS : "autoriza"
    DEVICES ||--o{ CHECKINS : "registra"

    PLANS {
        uuid item_id PK "items de tipo plan"
        int duration_days
        int freeze_max_days
        jsonb access_rules "días y horarios permitidos"
        text renewal_mode "manual, reminder"
    }
    MEMBERSHIPS {
        uuid id PK
        uuid space_id FK
        uuid customer_id FK
        uuid plan_id FK
        uuid commitment_id FK "kernel.commitments de acceso"
        uuid sale_id FK
        date starts_on
        date ends_on
        numeric price
        text status "upcoming, active, frozen, expired, canceled"
        text cancel_reason
        timestamptz canceled_at
    }
    MEMBERSHIP_FREEZES {
        uuid id PK
        uuid membership_id FK
        date frozen_from
        date frozen_until
        uuid requested_by FK
    }
    ACCESS_CREDENTIALS {
        uuid id PK
        uuid space_id FK
        uuid customer_id FK
        text kind "qr, nfc, document"
        text code "único por espacio"
        timestamptz issued_at
        timestamptz revoked_at
    }
    CHECKINS {
        uuid id PK
        uuid space_id FK
        uuid customer_id FK "nulo si el código no existe"
        uuid membership_id FK
        uuid credential_id FK
        uuid device_id FK
        text method "qr, nfc, document, manual"
        text scanned_value
        bool allowed
        text reason "expired, frozen, not_started, unknown"
        uuid recorded_by FK
        timestamptz at
    }
    DEVICES {
        uuid id PK
        uuid space_id FK
        text name
        text kind "reception_mobile"
        timestamptz last_sync_at
    }
```

## Reglas e invariantes

- **Una venta, una forma:** POS, enlace, membresía y mostrador crean `sales` con la misma operación.
- **La membresía se cobra con ventas:** nunca tiene su propio dinero; su `sale_id` apunta a la venta.
- **Caja cuadrada:** `expected_amount` se calcula de las entradas del libro en la cuenta de la caja durante la sesión.
- **Documentos inmutables:** un documento emitido se anula con nota crédito; nunca se edita.
- **Pagos externos una sola vez:** `provider_events.external_id` es único.
- **Entradas siempre registradas:** permitidas, rechazadas y códigos desconocidos.

## Fuera de esta etapa

Variantes, bodegas y existencias (patrón Productos), mesas y comandas (patrón Preparación), compras y proveedores, contabilidad completa y facturación electrónica. Cada uno tendrá su sección cuando entre a construcción.
