# Diseño: HU-kernel-resources-07

## Resumen
Se crean `kernel.resources` y `kernel.accounts` con RLS forzada, grants por columnas y claves foráneas compuestas que impiden referencias entre espacios y entre recursos. En Go, `kernel/state` ofrece `CreateResource`, `OpenAccount`, `CloseAccount` y dos lecturas; el rechazo de asientos en cuentas cerradas se completa en exchanges-08.

## Tipo de trabajo
`backend`: solo SQL y Go del kernel, sin contrato público ni app. Estado: lista, sin bloqueos.

## Módulos a tocar
- `docs/02-arquitectura/modelo-de-datos/kernel.md`: sección 2 (columna `closed_at`, rangos de `scale`) y Reglas (recurso inmutable para la API, cuenta que no se reabre, claves compuestas, unicidades).
- `docs/product/backlog/nucleo-del-kernel.md`: decisiones de abajo y estado de la historia.
- `docs/ingenieria/sprint-01/seguimientos.md`: filas nuevas (ver Riesgos).

## Archivos nuevos
- `kernel/db/migrations/00007_resources_accounts.sql`: tablas, claves, RLS, grants y disparador.
- `kernel/model/resource.go`: `ResourceKind`, `Resource`, con `Valid()`.
- `kernel/model/account.go`: `AccountClass`, `AccountStatus`, `Account`, con `Valid()`.
- `kernel/state/resource.go`: `CreateResource`, `GetResource`, validación y traducción de errores.
- `kernel/state/account.go`: `OpenAccount`, `CloseAccount`, `GetAccount`.
- Pruebas: `kernel/model/resource_test.go`, `kernel/db/resources_schema_test.go`, `kernel/state/resource_test.go`, `kernel/state/account_test.go`.
Cuatro archivos Go y no uno: sigue la separación de `participant.go` y `event.go`; recursos y cuentas tienen ciclos de vida distintos.

## Cambios de datos
Migración `00007_resources_accounts.sql` (la siguiente libre tras 00006):
```sql
-- +goose Up
CREATE TABLE kernel.resources (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  kind text NOT NULL CONSTRAINT resources_kind_check CHECK (kind IN ('money','unit','time','access')),
  code text NOT NULL CONSTRAINT resources_code_check CHECK (code ~ '^[A-Z][A-Z0-9_]{0,31}$'),
  name text NOT NULL CHECK (btrim(name) <> ''),
  scale integer NOT NULL CONSTRAINT resources_scale_check CHECK (scale BETWEEN 0 AND 18),
  CONSTRAINT resources_space_code_key UNIQUE (space_id, code),
  -- Target of the composite keys of accounts: a resource must live in the space of its account.
  CONSTRAINT resources_id_space_key UNIQUE (id, space_id)
);
CREATE TABLE kernel.accounts (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  holder_participant_id uuid NOT NULL,
  resource_id uuid NOT NULL,
  parent_account_id uuid,
  class text NOT NULL CONSTRAINT accounts_class_check
    CHECK (class IN ('asset','liability','equity','income','expense')),
  code text NOT NULL CHECK (btrim(code) <> ''),
  name text NOT NULL CHECK (btrim(name) <> ''),
  status text NOT NULL DEFAULT 'open' CONSTRAINT accounts_status_check CHECK (status IN ('open','closed')),
  closed_at timestamptz,
  CONSTRAINT accounts_closed_coherent CHECK ((status = 'closed') = (closed_at IS NOT NULL)),
  CONSTRAINT accounts_not_own_parent CHECK (parent_account_id IS NULL OR parent_account_id <> id),
  CONSTRAINT accounts_holder_code_key UNIQUE (space_id, holder_participant_id, code),
  -- Target of the parent key and of the ledger entries of exchanges-08: same space and same resource.
  CONSTRAINT accounts_id_space_resource_key UNIQUE (id, space_id, resource_id),
  CONSTRAINT accounts_holder_fkey FOREIGN KEY (holder_participant_id, space_id)
    REFERENCES kernel.participants (id, space_id),
  CONSTRAINT accounts_resource_fkey FOREIGN KEY (resource_id, space_id)
    REFERENCES kernel.resources (id, space_id),
  -- MATCH SIMPLE: with a null parent the key is not checked.
  CONSTRAINT accounts_parent_fkey FOREIGN KEY (parent_account_id, space_id, resource_id)
    REFERENCES kernel.accounts (id, space_id, resource_id)
);
```
`participants_id_space_key` ya existe (00006); no se vuelve a crear.
- RLS: `ENABLE` y `FORCE` en ambas. `resources`: políticas `FOR SELECT` y `FOR INSERT` con `space_id = (SELECT kernel.current_space_id())` (sin política de `UPDATE`/`DELETE`). `accounts`: `SELECT`, `INSERT` y `UPDATE` (`USING` y `WITH CHECK` igual); sin `DELETE`.
- Grants, todos por columnas, sin `ALTER DEFAULT PRIVILEGES`: `resources`: `SELECT` y `INSERT (id, space_id, kind, code, name, scale)`; nada de `UPDATE` (cambiar `scale` o `kind` tras haber asientos corrompería el libro). `accounts`: `SELECT`, `INSERT (id, space_id, holder_participant_id, resource_id, parent_account_id, class, code, name)` (sin `status` ni `closed_at`: toda cuenta nace abierta) y `UPDATE (status, closed_at)`. Ningún `DELETE`/`TRUNCATE`. Sin secuencias ni columnas generadas aquí; aun así el grant por columnas evita que una columna futura quede concedida sin querer (lección de events-09).
- Disparador `kernel.reject_account_reopen()` (`BEFORE UPDATE` por fila, `search_path = pg_catalog, pg_temp`, `restrict_violation`): si `OLD.status = 'closed'` y `NEW.status <> 'closed'` o cambia `closed_at`, falla, incluso para el dueño. Sin él, el `UPDATE (status)` concedido permitiría reabrir y anular el criterio 6 (mismo patrón que `reject_event_change`).
- `Down`: borra disparador, función, `accounts` y `resources`, en ese orden.
- Estados: cuenta `open -> closed`, irreversible. Recursos: sin estados.

## Contrato
```go
// model
type ResourceKind string   // money, unit, time, access
type AccountClass string   // asset, liability, equity, income, expense
type AccountStatus string  // open, closed
type Resource struct { ID, SpaceID string; Kind ResourceKind; Code, Name string; Scale int }
type Account struct { ID, SpaceID, HolderParticipantID, ResourceID, ParentAccountID string
    Class AccountClass; Code, Name string; Status AccountStatus; ClosedAt *time.Time }
// state
type NewResource struct { Kind model.ResourceKind; Code, Name string; Scale int }
type NewAccount struct { HolderParticipantID, ResourceID, ParentAccountID string
    Class model.AccountClass; Code, Name string }
func CreateResource(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID string, in NewResource) (string, error)
func OpenAccount(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID string, in NewAccount) (string, error)
func CloseAccount(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID, accountID string) error
func GetResource(ctx context.Context, pool *sql.DB, spaceID, resourceID string) (model.Resource, error)
func GetAccount(ctx context.Context, pool *sql.DB, spaceID, accountID string) (model.Account, error)
```
- Las tres escrituras: `actor.Kind != system` devuelve `permission_denied` antes de tocar nada. Validan antes de abrir transacción, generan el UUID v7 en Go y toman el espacio de `kernel.current_space_id()`, como `CreateParticipant`; usan `InSpace`.
- `invalid_input`: `kind` o `class` desconocidos, `scale` fuera de 0..18, `code` de recurso fuera de `^[A-Z][A-Z0-9_]{0,31}$`, `name`/`code` de cuenta en blanco, IDs no UUID o nulos (`ParentAccountID` vacío = sin madre). Los `CHECK` (23514) y 22P02 se traducen igual como respaldo.
- `rule_violation`: 23505 (`resources_space_code_key`, `accounts_holder_code_key`) y 23503 de `accounts_holder_fkey`, `accounts_resource_fkey`, `accounts_parent_fkey` (otro espacio, otro recurso o inexistente). 23503 de `*_space_id_fkey` es `not_found`. RLS de `INSERT` 42501 se deja sin tipo (envuelto): no se alcanza desde Go.
- `CloseAccount`: `UPDATE ... SET status='closed', closed_at=now() WHERE id=$1 AND status='open'`; si no afecta filas, comprueba existencia: cerrada es éxito idempotente (`closed_at` no cambia), invisible es `not_found` (patrón de `ArchiveParticipant`). Las lecturas devuelven `not_found` si el espacio no ve la fila.
- Contrato para exchanges-08 (criterio 6): (a) `entries` referencia `accounts (id, space_id, resource_id)` con la clave `accounts_id_space_resource_key`; eso resuelve su criterio 5 (recurso distinto) en SQL. (b) un disparador `BEFORE INSERT` en `entries` lee `accounts.status` con `SELECT ... FOR SHARE` y falla con `rule_violation` (en Go) si es `closed`; el bloqueo serializa con un `CloseAccount` concurrente. (c) 08 añade la prueba "cuenta cerrada rechaza asiento" y otra de carrera cierre/asiento.

## Pruebas requeridas
Primero y rojas; usan `dbtest.Database`, `dbtest.APIPool`, `seedSpaces`; fallan, no se omiten. Cada prueba de state prepara datos con el actor `system`.
1. Criterio 1: `TestCreateResourceStoresMoneyCOP` y `TestCreateResourceDuplicateCodeIsRuleViolation` (mismo código en el otro espacio sí se acepta) en `kernel/state/resource_test.go`; `TestResourcesUniqueSpaceCode` en `kernel/db/resources_schema_test.go`.
2. Criterio 2: `TestCreateResourceRejectsBadKindScaleCode` (tabla: kind `foo`, scale -1, scale 19, code `cop`, name en blanco; no escribe filas) en state; `TestResourceKindValid` en `kernel/model/resource_test.go`; `TestResourcesCheckConstraints` por SQL con el administrador en db.
3. Criterio 3: `TestOpenAccountStartsOpen` y `TestOpenAccountRejectsBadClass` (tabla con clases; también `AccountClass.Valid` en model) en `kernel/state/account_test.go`; `TestAccountsCheckConstraints` (clase, estado, `accounts_closed_coherent`, madre propia) en db.
4. Criterio 4: `TestOpenAccountParentOfOtherResourceIsRuleViolation`, `TestOpenAccountParentOfOtherSpaceIsRuleViolation` (el administrador inserta la madre en B), `TestOpenAccountWithParentSameResource` (éxito) en state; `TestAccountsParentFkeyIsComposite` (catálogo: columnas de la FK) en db.
5. Criterio 5: `TestOpenAccountHolderOfOtherSpaceIsRuleViolation`, `TestOpenAccountResourceOfOtherSpaceIsRuleViolation`; el caso "ajeno" usa un participante o recurso real de B, no un UUID inventado.
6. Criterio 6 (parte de esta historia): `TestCloseAccountSetsClosedAtAndIsIdempotent`, `TestCloseAccountOfOtherSpaceIsNotFound` en state; `TestClosedAccountCannotBeReopenedEvenByOwner` (administrador, `restrict_violation`, también cambiar `closed_at`) y `TestAPICannotInsertClosedAccount` en db. La parte de asientos queda en exchanges-08.
7. Criterio 7: `TestResourcesIsolationBetweenSpaces` y `TestAccountsIsolationBetweenSpaces` (B lee 0 filas; `GetResource` de A desde B da `not_found`), `TestInsertForeignSpaceRejected` (SQL con rol de la API, espacio B y `space_id = A`, 42501) en state; `TestResourcesAccountsForcedRLSAndGrants` en db (ENABLE, FORCE, solo las políticas previstas, `column_privileges` exactos, sin `DELETE`/`TRUNCATE`, sin `UPDATE` en `resources`, `UPDATE` solo de `status` y `closed_at`).
8. Criterio 8: `TestAmountColumnsAreNumeric` en `kernel/db/resources_schema_test.go`. Hoy las tablas del libro no existen, así que el test (a) recorre `information_schema.columns` del esquema `kernel` y exige `numeric` en toda columna llamada `amount` o terminada en `_amount`; (b) prohíbe `real`, `double precision` y `money` en todo el esquema; (c) exige que cada columna de la lista `expectedAmountColumns` exista (hoy vacía; exchanges-08 añade `entries.amount`, y 11/12 las suyas); (d) `TestAmountCheckDetectsFloat` crea una tabla con `amount double precision` en una base desechable y comprueba que el verificador la marca, para que el test no sea vacío. También exige `resources.scale` de tipo `integer`. La comprobación completa del criterio ocurre en exchanges-08; hoy es una guarda de regresión.
9. Arquitectura: `kernel/architecture_test.go` y `kernel/db/adr_test.go` siguen verdes (sin vocabulario de negocio; `money` y `income` no están prohibidos).

## Riesgos técnicos y mitigación
- **Dinero en Go.** Esta historia no mueve montos: `scale` es `int` y no hace falta tipo decimal. Se difiere a exchanges-08. Recomendación para esa historia: montos como cadena decimal validada, convertida a `math/big` (entero sin escala más escala) para sumar y comparar contra `resources.scale`, sin dependencia nueva, ni `float`; la alternativa `shopspring/decimal` sirve, pero añade una dependencia para solo sumar y comparar. Decidirlo allí con una nota en la historia.
- **FK ignora RLS.** Por eso todas las claves hacia datos de espacio son compuestas con `space_id`; la prueba de catálogo comprueba sus columnas.
- **`scale` inmutable solo para la API.** El dueño podría cambiarla con DDL o `UPDATE`. exchanges-08 debe añadir un disparador que impida cambiar `scale` y `kind` si existen asientos. Anotar en seguimientos.
- **Titular archivado.** Se permite abrir cuentas para un participante archivado y cerrar una cuenta con hijas abiertas; la historia no lo prohíbe. Anotar en seguimientos.
- **Evolución de grants.** El grant por columnas de `accounts` se actualiza a mano al añadir columnas; la prueba con `column_privileges` lo detecta (mismo riesgo que el seguimiento 15).

## Contradicciones historia vs documentación (con recomendación)
1. `kernel.md` no tiene `closed_at`; la historia dice "cerrada". Recomendación: añadir `closed_at` coherente con `status` (igual que `archived_at`) y documentarlo.
2. Criterio 3 no menciona `name` de cuenta, pero el modelo lo tiene. Recomendación: obligatorio y no vacío; es una interpretación a confirmar en el PR.
3. La historia fija unicidad solo para recursos. Para cuentas, sin regla el código repetido sería ambiguo. Recomendación: `(space_id, holder_participant_id, code)` como regla nueva (`rule_violation`).
4. La historia dice "`scale` negativo"; no hay tope. Recomendación: 0 a 18 y `code` de recurso en mayúsculas (`COP`, `UND`, `MIN`), reglas nuevas documentadas.
5. Criterio 6 pide verificar en exchanges-08 lo que depende de esta tabla. Recomendación: aquí estado, transición, no reabrir; allá el rechazo de asientos (contrato arriba).
6. Criterio 8 presupone tablas del libro que aún no existen. Recomendación: guarda de catálogo hoy y verificación completa en 08 (prueba 8).

## Fuera de alcance
`account_balances`, intercambios y asientos (08), tipo decimal en Go, conversión entre recursos, particionado del libro (ADR-0018), permisos y capacidades (grants-05, execution-13), eventos de creación y cierre (execution-13), reapertura de cuentas, borrado, listados y paginación, jerarquía profunda o consolidación de saldos.
