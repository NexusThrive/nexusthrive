# Diseño: HU-kernel-exchanges-08

## Resumen
Se crean `kernel.exchanges` y `kernel.entries` (libro de doble partida) con conservación diferida por `(exchange_id, resource_id)`, inmutabilidad en dos capas, rechazo de cuentas cerradas con `FOR SHARE`, escala validada por recurso y compensación única con asientos espejo. Se cierran los seguimientos 17 (escala y tipo de recurso congelados con asientos) y 19 (`closed_at` fijado por el kernel). Estado: lista, sin bloqueos; las contradicciones están al final de Riesgos.

## Tipo de trabajo
`backend`: solo SQL y Go del kernel; sin contrato público ni app.

## Módulos a tocar
- `kernel/db/resources_schema_test.go`: `expectedAmountColumns = []string{"entries.amount"}`; añadir pruebas de seguimientos 17 y 19.
- `kernel/state/account.go`: actualizar el comentario de `CloseAccount` (ya no "pertenece a la historia del libro").
- `docs/02-arquitectura/modelo-de-datos/kernel.md`: sección 2 y Reglas (ver Cambios de datos).
- `docs/product/backlog/nucleo-del-kernel.md`: estado y decisiones. `docs/ingenieria/sprint-01/seguimientos.md`: cerrar 17 y 19, filas nuevas (Riesgos).

## Archivos nuevos
- `kernel/db/migrations/00009_ledger.sql`: tablas, RLS, grants, disparadores y los dos reemplazos (cuentas, recursos).
- `kernel/model/amount.go`: `Amount` decimal exacto. `kernel/model/exchange.go`: `NewExchange`, `NewEntry`, `Exchange`, `Entry`, `Validate()`.
- `kernel/state/exchange.go`: `AppendExchange`, `RecordExchange`, `AppendReversal`, `ReverseExchange`, `GetExchange`, `translateExchangeError`. Un archivo: mismo ciclo de vida que `event.go`.
- Pruebas: `kernel/model/amount_test.go`, `kernel/model/exchange_test.go`, `kernel/db/exchanges_schema_test.go`, `kernel/state/exchange_test.go`.

## Cambios de datos
Migración `00009_ledger.sql` (siguiente libre tras 00008). Todo en inglés; el vocabulario del kernel no incluye palabras de negocio (los ejemplos de `kind` usan `test.exchange`).
```sql
-- +goose Up
CREATE TABLE kernel.exchanges (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  kind text NOT NULL CHECK (char_length(kind) <= 128 AND kind ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'),
  idempotency_key text NOT NULL CHECK (btrim(idempotency_key) <> '' AND char_length(idempotency_key) <= 255),
  actor_participant_id uuid NOT NULL,
  reverses_exchange_id uuid,
  memo text CHECK (memo IS NULL OR btrim(memo) <> ''),
  metadata jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(metadata) = 'object'),
  occurred_at timestamptz NOT NULL DEFAULT now(),
  recorded_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT exchanges_idempotency_key UNIQUE (space_id, idempotency_key),
  CONSTRAINT exchanges_reverses_once_key UNIQUE (space_id, reverses_exchange_id), -- NULLs never collide
  CONSTRAINT exchanges_id_space_key UNIQUE (id, space_id),
  CONSTRAINT exchanges_not_self_reverse CHECK (reverses_exchange_id IS NULL OR reverses_exchange_id <> id),
  CONSTRAINT exchanges_actor_fkey FOREIGN KEY (actor_participant_id, space_id) REFERENCES kernel.participants (id, space_id),
  CONSTRAINT exchanges_reverses_fkey FOREIGN KEY (reverses_exchange_id, space_id) REFERENCES kernel.exchanges (id, space_id));
CREATE TABLE kernel.entries (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  exchange_id uuid NOT NULL, space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  account_id uuid NOT NULL, resource_id uuid NOT NULL,
  -- Rejects zero, NaN and infinities (NaN and Infinity compare above every finite value).
  amount numeric NOT NULL CONSTRAINT entries_amount_check CHECK (amount <> 0 AND amount < 1e30 AND amount > -1e30),
  CONSTRAINT entries_exchange_fkey FOREIGN KEY (exchange_id, space_id) REFERENCES kernel.exchanges (id, space_id),
  CONSTRAINT entries_account_fkey FOREIGN KEY (account_id, space_id, resource_id) REFERENCES kernel.accounts (id, space_id, resource_id));
CREATE INDEX entries_exchange_idx ON kernel.entries (exchange_id);
CREATE INDEX entries_account_idx ON kernel.entries (space_id, account_id);
```
- RLS `ENABLE` y `FORCE` en ambas; políticas `FOR SELECT` y `FOR INSERT` con `space_id = (SELECT kernel.current_space_id())`; sin política de `UPDATE`/`DELETE`.
- Grants por columnas, sin `ALTER DEFAULT PRIVILEGES`: `SELECT` en ambas; `INSERT (id, space_id, kind, idempotency_key, actor_participant_id, reverses_exchange_id, memo, metadata, occurred_at)` en `exchanges` (sin `recorded_at`); `INSERT (id, exchange_id, space_id, account_id, resource_id, amount)` en `entries`. Nada de `UPDATE`, `DELETE`, `TRUNCATE`, `REFERENCES`. `commitment_id` no existe aún: lo añade commitments-11 con su grant.
- Todas las funciones: `LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp`, objetos siempre como `kernel.x`, sin `SECURITY DEFINER`. Los errores usan `RAISE ... USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = '<nombre>'` para que Go distinga la regla por `pgErr.ConstraintName`.
1. `kernel.reject_ledger_change()`: `restrict_violation`. Disparadores `BEFORE UPDATE OR DELETE ... FOR EACH ROW` y `BEFORE TRUNCATE ... FOR EACH STATEMENT` en ambas tablas (segunda capa de memoria; incluso para dueño y administrador, y también en `TRUNCATE ... CASCADE` desde `spaces`, `accounts` o `participants`, porque el disparador de las tablas arrastradas se ejecuta).
2. `kernel.stamp_exchange()` `BEFORE INSERT` en `exchanges`: `NEW.recorded_at := now()` (nadie, ni el dueño, fija la hora de registro; la API ni siquiera tiene el grant).
3. `kernel.guard_entry_insert()` `BEFORE INSERT` en `entries`, en este orden:
   a. el intercambio debe haberse creado en esta misma transacción: `EXISTS (SELECT 1 FROM kernel.exchanges e WHERE e.id = NEW.exchange_id AND e.space_id = NEW.space_id AND e.xmin = pg_current_xact_id()::xid)` o `entries_same_transaction`. Impide añadir asientos balanceados a un intercambio ya confirmado (la historia no se reescribe) y elimina la carrera de dos transacciones sobre el mismo intercambio (la fila no es visible para otra transacción, así que su clave foránea falla). Con savepoints falla cerrado (rechaza, nunca acepta de más);
   b. `SELECT a.status, a.resource_id INTO ... FROM kernel.accounts a WHERE a.id = NEW.account_id AND a.space_id = NEW.space_id FOR SHARE`: si no hay fila, o `status <> 'open'`, `entries_account_open`; si `a.resource_id <> NEW.resource_id`, `entries_account_resource`. `FOR SHARE` choca con el `UPDATE` de `CloseAccount` (`FOR NO KEY UPDATE`) y no entre asientos; la clave foránea solo toma `KEY SHARE`, que no choca con el cierre, por eso no basta. La API tiene `UPDATE (status, closed_at)` y política de `UPDATE`, requisito de `FOR SHARE`. El filtro de estado se evalúa en PL, no en el `WHERE`, para ver la versión ya cerrada tras esperar;
   c. escala: `SELECT scale` del recurso de la cuenta; si `NEW.amount <> round(NEW.amount, scale)` falla `entries_amount_scale` (por valor: `50.100` con escala 2 se acepta); luego `NEW.amount := round(NEW.amount, scale)` (se guarda canónico).
4. `kernel.check_exchange_balance()`, usada por `CREATE CONSTRAINT TRIGGER exchanges_balance_check AFTER INSERT ON kernel.exchanges DEFERRABLE INITIALLY DEFERRED FOR EACH ROW` y `entries_balance_check` igual sobre `entries` (el id del intercambio es `NEW.id` o `NEW.exchange_id` según `TG_TABLE_NAME`). Comprueba sobre `kernel.entries`: (i) `count(*) >= 2` o `exchanges_min_entries`; (ii) ningún `GROUP BY resource_id HAVING sum(amount) <> 0` o `exchanges_conservation`; (iii) si `reverses_exchange_id` no es nulo, `FULL JOIN` de `sum(amount) GROUP BY account_id` del intercambio contra el original, y `coalesce(x.s,0) <> -coalesce(o.s,0)` en alguna fila da `exchanges_reversal_mirror`. El trigger en `exchanges` es el que rechaza el intercambio vacío (criterio 4): sobre `entries` nunca se dispararía sin filas. El de `entries` queda como defensa en profundidad.
5. `kernel.guard_resource_change()` `BEFORE UPDATE` en `resources` (seguimiento 17): si cambia `scale` o `kind`, exige `kernel.current_space_id() IS NOT DISTINCT FROM OLD.space_id` (la RLS forzada filtra los asientos; sin esto el dueño vería cero y pasaría), hace `LOCK TABLE kernel.entries IN SHARE MODE` (espera a los insertores en vuelo) y si existe un asiento del recurso falla con `restrict_violation`.
6. `CREATE OR REPLACE FUNCTION kernel.reject_account_reopen()` (seguimiento 19): conserva la regla de no reabrir y añade `IF OLD.status = 'open' AND NEW.status = 'closed' THEN NEW.closed_at := now(); END IF`. Los `BEFORE` corren antes de `accounts_closed_coherent`. El disparador existente se conserva.
- `Down`: borra disparadores y funciones nuevas, `entries`, `exchanges`, restaura el cuerpo previo de `reject_account_reopen`.
- Estados: no hay; las filas del libro son inmutables. `kernel.md` se actualiza: `idempotency_key` y `actor_participant_id` NOT NULL, `reverses` único por espacio, `metadata` por defecto `{}`, `commitment_id` llega con commitments-11.

## Contrato
```go
// model: Amount es decimal exacto (math/big, sin float, sin dependencia nueva).
type Amount struct{ /* entero sin escala y escala, normalizado sin ceros finales */ }
func ParseAmount(s string) (Amount, error) // ^-?(0|[1-9][0-9]{0,29})(\.[0-9]{1,18})?$; sin "+", exponente, NaN, espacios; invalid_input
func (a Amount) String() string; Neg() Amount; Add(b Amount) Amount; Sign() int; IsZero() bool; FitsScale(scale int) bool
type NewEntry struct{ AccountID, ResourceID string; Amount Amount }
type NewExchange struct{ Kind, IdempotencyKey, ReversesExchangeID, Memo string; Metadata json.RawMessage
    OccurredAt time.Time; Entries []NewEntry }
type Entry struct{ ID, ExchangeID, AccountID, ResourceID string; Amount Amount }
type Exchange struct{ ID, SpaceID, Kind, IdempotencyKey, ActorParticipantID, ReversesExchangeID, Memo string
    Metadata json.RawMessage; OccurredAt, RecordedAt time.Time /* UTC */; Entries []Entry }
func (e NewExchange) Validate() error
// state
func AppendExchange(ctx context.Context, tx *sql.Tx, actor model.Actor, in model.NewExchange) (model.Exchange, error)
func RecordExchange(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID string, in model.NewExchange) (model.Exchange, error)
func AppendReversal(ctx context.Context, tx *sql.Tx, actor model.Actor, exchangeID, idempotencyKey string) (model.Exchange, error)
func ReverseExchange(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID, exchangeID, idempotencyKey string) (model.Exchange, error)
func GetExchange(ctx context.Context, pool *sql.DB, spaceID, exchangeID string) (model.Exchange, error)
```
- `Append*` siguen a `AppendEvent`: no abren, confirman ni revierten; sin savepoints; el espacio sale de `kernel.current_space_id()` (sin espacio: `rule_violation` antes de escribir); validan antes de tocar la base. Un error de base deja la transacción abortada y el llamador la revierte. execution-13 usa `AppendExchange` en su transacción única. `RecordExchange` y `ReverseExchange` abren su transacción con `InSpace` (pruebas y uso autónomo).
- Validación (sin escribir): actor distinto de `system` o sin `ParticipantID` es `permission_denied` (sin escrituras anónimas; la restricción a `system` sigue a `OpenAccount` hasta execution-13); `kind`, clave (1 a 255, no en blanco), IDs UUID no nulos, metadata objeto JSON, montos no cero, de 2 a 100 asientos: `invalid_input`. IDs de intercambio y asientos UUID v7 en Go.
- Tras insertar, `AppendExchange` ejecuta `SET CONSTRAINTS kernel.exchanges_balance_check, kernel.entries_balance_check IMMEDIATE` y luego `... DEFERRED`: el error de conservación sale tipado en la llamada y no al confirmar el llamador. Los disparadores diferidos siguen siendo la garantía final (las pruebas SQL los ejercen en `COMMIT`).
- `AppendReversal`: lee los asientos del original (la RLS oculta otro espacio: `not_found`), los invierte con `Neg()` y registra con `kind = "kernel.exchange.reversal"` y `reverses_exchange_id`. No hay verificación previa de "ya compensado": decide el índice único, que es seguro ante la carrera.
- Traducción por `ConstraintName` y luego por código: `exchanges_idempotency_key` y `exchanges_actor_fkey`, `entries_account_fkey`, `entries_account_open`, `entries_account_resource`, `entries_same_transaction`, `exchanges_conservation`, `exchanges_min_entries`, `exchanges_reversal_mirror`: `rule_violation`. `exchanges_reverses_once_key`: `already_done`. `exchanges_reverses_fkey` y `*_space_id_fkey`: `not_found`. `entries_amount_scale`, 23514, 22P02: `invalid_input`. 23502 sin espacio: `rule_violation`. El resto, envuelto y sin tipo. Los errores de `Commit` también se traducen (`errors.As` atraviesa el envoltorio).

## Pruebas requeridas
Todas primero y en rojo, con PostgreSQL real (`dbtest.Database`, `dbtest.APIPool`, `seedSpaces`); fallan, no se omiten. Orden: 1) `model/amount_test.go` y `exchange_test.go`; 2) `db/exchanges_schema_test.go`; 3) `state/exchange_test.go`. En db, el SQL crudo abre transacciones con `BEGIN ... COMMIT` y comprueba el error del `COMMIT`. Cada una debe fallar si se quita su disparador, grant o restricción.
- C1: `TestBalancedExchangeCommitsWithUTCRecordedAt` (db; `recorded_at` entre el instante previo y posterior) y `TestRecordExchangeStoresExchangeAndEntries` (state; `"50.00"` y `"-50.00"` ida y vuelta, `RecordedAt` en UTC).
- C2: `TestUnbalancedExchangeFailsAtCommitLeavesNoRows` (db; +50.00 y -49.99: el `INSERT` pasa, falla `COMMIT` con `exchanges_conservation`, cero filas en ambas tablas), `TestBalanceChecksAreDeferrable` (catálogo: `condeferrable` y `condeferred`; el primer asiento solo no falla), `TestAppendExchangeUnbalancedIsRuleViolation` (state).
- C3: `TestBalanceIsPerResource` (db; R1 suma 0 y R2 +1.00 se rechaza; R1 +1 con R2 -1 también, aunque el total global sea 0).
- C4: `TestExchangeWithoutEntriesRejectedAtCommit`, `TestExchangeWithSingleEntryRejected`, `TestZeroNaNInfinityAmountsRejected` (db), `TestNewExchangeValidateRejectsFewOrZeroEntries` (model).
- C5: `TestEntryOnAccountOfOtherResourceIsRuleViolation`, `TestEntryOnClosedAccountIsRuleViolation`, `TestEntryOnAccountOfOtherSpaceIsRuleViolation` (cuenta real de B), en state; `TestCloseAccountRacesWithEntry` en db o state, ambos órdenes: (a) la transacción del asiento retiene el bloqueo y `CloseAccount` espera (comprobar con `pg_stat_activity` `wait_event_type = 'Lock'`), luego ambos terminan; (b) un cierre sin confirmar retiene la fila, el asiento espera y al confirmar el cierre recibe `rule_violation`. Sin `FOR SHARE`, (b) inserta en cuenta cerrada.
- C6: `TestAmountBeyondResourceScaleIsInvalidInput` (state; `"10.001"` en escala 2; `"10.100"` se guarda como `10.10`), `TestEntryScaleEnforcedByDatabase` (db, SQL directo con el rol de la API), `TestAmountParse*` (model: rechazos de `1e3`, `+1`, `NaN`, `1.`, 31 enteros, 19 decimales; suma exacta de `0.1`+`0.2` es `0.3`).
- C7: `TestLedgerIsAppendOnly` (db; API: `UPDATE`, `DELETE`, `TRUNCATE` dan 42501; administrador: `restrict_violation` en ambas tablas, incluido `TRUNCATE ... CASCADE` de `exchanges`, de `accounts` y de `spaces`; en base desechable con `GRANT UPDATE, DELETE` concedido por error los disparadores siguen rechazando), `TestEntryCannotJoinCommittedExchange` (intercambio confirmado, nueva transacción inserta par balanceado: `entries_same_transaction`).
- C8: `TestReverseExchangeNetsToZeroPerAccount` (saldo neto 0 por cuenta), `TestReverseTwiceIsAlreadyDone`, `TestConcurrentReversalsOnlyOneWins` (dos goroutines, claves distintas), `TestReverseExchangeOfOtherSpaceIsNotFound`, `TestReversalMustMirrorOriginal` (db; montos, cuentas o conjunto distintos fallan en `COMMIT`; espejo exacto pasa).
- C9: `TestIdempotencyKeyUniquePerSpace` (db y state: misma clave en A es `rule_violation`, en B se acepta; dos goroutines con la misma clave, solo una gana).
- C10: `TestLedgerIsolationBetweenSpaces` (B lee 0 filas en ambas; `GetExchange` de A desde B es `not_found`; B inserta con `space_id` de A: 42501; B inserta asientos sobre un intercambio de A: falla), `TestLedgerForcedRLSAndGrants` (ENABLE, FORCE, solo `SELECT`/`INSERT`, `column_privileges` exactos, `recorded_at` no concedido, conteo de columnas del grant).
- C11: `TestExchangeWithoutActorRejected` (db: NULL da 23502; state: `permission_denied`; actor `person` también `permission_denied`; actor de otro espacio `rule_violation`).
- Ataques: `TestAPIRoleCannotForgeRecordedAt` (el `INSERT` con `recorded_at` da 42501; el dueño que lo fija recibe `now()`), `TestSpaceSwitchBeforeCommitFailsClosed`, `TestOnConflictDoNothingCannotReuseCommittedExchange`, `TestCopyFromStillFiresTriggers` (`CopyFrom` de pgx con un asiento sin contrapartida falla al confirmar).
- Seguimientos: `TestResourceScaleAndKindFrozenOnceEntriesExist` (administrador; error exacto `restrict_violation`, no cero filas; con espacio actual distinto también falla; sin asientos se permite), `TestAPICannotForgeClosedAt` (rol de la API fija `closed_at = '2000-01-01'`; queda igual a `now()` de la transacción), `TestAmountColumnsAreNumeric` actualizado. `kernel/architecture_test.go` y `adr_test.go` en verde.

## Riesgos técnicos y mitigación
Vectores considerados (rol de la API, atacante): `OVERRIDING SYSTEM VALUE`: no hay identidades ni secuencias en estas tablas. `COPY`: los disparadores `BEFORE` y diferidos corren igual (prueba). `ON CONFLICT DO UPDATE`: sin privilegio de `UPDATE`; `DO NOTHING` sobre un id confirmado no inserta y los asientos caen en `entries_same_transaction`. `TRUNCATE` y `TRUNCATE CASCADE`: sin privilegio y con disparador (también en cascada). `SET ROLE`: la API no es miembro de otro rol. `ALTER TABLE` y `DISABLE TRIGGER`: requieren ser dueño; límite aceptado de ADR-0028, auditoría de DDL en B6. `recorded_at` falsificado: sin grant y con disparador. `closed_at` falsificado: disparador. Cambiar `kernel.space_id` a mitad de transacción: la RLS vacía las lecturas de los disparadores y estos fallan cerrados (prueba). Funciones o temporales que suplanten objetos: `search_path` fijo y nombres calificados. Asiento en cuenta o recurso ajeno: claves compuestas más el disparador. Asientos nuevos en un intercambio confirmado: `entries_same_transaction`. Compensar dos veces: índice único. `NaN` e infinitos: `CHECK`. Intercambio vacío: disparador diferido en `exchanges`.
- **Actor afirmado por la aplicación.** La base solo garantiza que el actor existe en el espacio; la API podría escribir cualquier participante del espacio. Aceptado en M1; el vínculo con la identidad llega con permisos (execution-13). Seguimiento nuevo.
- **Compensar con cuenta cerrada.** Una cuenta cerrada tras E1 impide compensar E1 (criterio 5 sin excepción, cuentas que no se reabren). Recomendación: mantener; decidir una excepción en una ADR si aparece. Seguimiento nuevo.
- **Coste O(N^2) en el disparador de asientos** (corre por fila): se acota con 100 asientos por intercambio.
- **Restauración de respaldos (B6).** Los disparadores `BEFORE` de `entries` rechazarían recargar asientos antiguos; el restaurado debe usar `--disable-triggers` con superusuario. Seguimiento nuevo.
- **`xmin` y savepoints.** Falla cerrado, nunca abierto. Si un llamador usa savepoints para el intercambio, recibe `entries_same_transaction`; `AppendExchange` no los usa.
- **`REPEATABLE READ`.** El `FOR SHARE` puede dar 40001 contra un cierre; no se traduce, el llamador reintenta. Hoy todo corre en `READ COMMITTED`.
- **`LOCK TABLE` en el cambio de recurso** no tiene prueba de carrera (operación excepcional del dueño); se documenta.
- Seguimiento 18 (titular archivado, hijas abiertas) se mantiene abierto: no lo exigen los criterios.
- **Contradicciones historia vs documentación (recomendación).** 1) `kernel.md` pone `commitment_id` en `entries`; se pospone a commitments-11. 2) Eventos admiten actor `system` sin participante; aquí el criterio 11 exige participante siempre: un actor `system` necesita un participante de tipo `system`; sin él, `permission_denied`. 3) Criterio 4 admite "asiento distinto de cero": se prohíben montos cero con `CHECK` y se exigen 2 asientos. 4) Criterio 9 dice "rechaza"; idempotency-14 lo cambiará a devolver el resultado original, y entonces se ajustan esas pruebas. 5) `idempotency_key` no declara nulabilidad: NOT NULL (operaciones de dinero siempre idempotentes). 6) Criterio 7 cubre `UPDATE` y `DELETE` de `entries`; se amplía a `TRUNCATE` y a `exchanges`. 7) "Invertir los originales" se interpreta por suma neta por cuenta. 8) La nota supera las 150 líneas pedidas por el rol por exigencia explícita de contenido (SQL, ataques, pruebas).

## Fuera de alcance
`commitment_id` y cumplimientos (commitments-11), eventos y outbox (execution-13), permisos y capacidades, el motor de idempotencia que devuelve el resultado original (idempotency-14), `account_balances`, conversión entre recursos, particionado (ADR-0018), pruebas de leyes en `kernel/laws/` (laws-15, reutilizarán estas), listados y paginación, límite de tamaño de `metadata`.
