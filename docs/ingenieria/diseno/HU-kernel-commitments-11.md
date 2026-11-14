# Diseño: HU-kernel-commitments-11

## Resumen
Se crean `kernel.commitments` y `kernel.fulfillments` (migración `00012_commitments.sql`). El estado, `fulfilled_amount` y `closed_at` los deriva el kernel con disparadores (la API no los fija), el cumplimiento serializa con `FOR NO KEY UPDATE` sobre el compromiso y cada asiento cumple un solo compromiso, completo. Estado: lista, sin bloqueos; las contradicciones con la historia y la documentación están en Riesgos. La nota supera las 150 líneas por exigencia explícita de contenido (SQL, ataques, pruebas), como exchanges-08.

## Tipo de trabajo
`backend`: SQL y Go del kernel; sin contrato público ni app.

## Módulos a tocar
- `kernel/db/migrations/00011_*` no se toca. `kernel/db/resources_schema_test.go`: `expectedAmountColumns` suma `commitments.amount`, `commitments.fulfilled_amount`, `fulfillments.amount`; conteos de columnas de los grants.
- `docs/02-arquitectura/modelo-de-datos/kernel.md`: sección 3 (ER de `commitments`: añadir `created_by_participant_id`, `created_at`; de `fulfillments`: `space_id`, `resource_id`) y Reglas (Compromiso y Libro: `commitment_id` NO se añade a `entries`). `docs/product/backlog/nucleo-del-kernel.md`: línea 296 y estado de la historia. `docs/ingenieria/sprint-01/seguimientos.md`: filas 51 a 55 (Riesgos).
- `kernel/db/migrations/00009_ledger.sql` NO se edita (ya fusionada): el reemplazo de `guard_resource_change` va en 00012.

## Archivos nuevos
- `docs/05-decisiones/0033-compromisos-estado-derivado-y-asiento-unico.md` (ADR antes del código; formato de 0032, lo valida `adr_test.go`): estado derivado por disparador, `fulfilled_amount` derivado y validado contra la suma, asiento único y completo, bloqueo de fila, `entries.commitment_id` descartado, alcance de timers-12.
- `kernel/db/migrations/00012_commitments.sql`: tablas, RLS, grants, disparadores, `UNIQUE` nuevo en `entries`, reemplazo de `guard_resource_change`.
- `kernel/model/commitment.go`: tipos, estados, `NewCommitment.Validate()`. `kernel/state/commitment.go`: operaciones y `translateCommitmentError` (mismo ciclo de vida, un archivo como `exchange.go`).
- Pruebas: `kernel/model/commitment_test.go`, `kernel/db/commitments_schema_test.go`, `kernel/db/commitments_security_test.go`, `kernel/state/commitment_test.go`, `kernel/state/commitment_concurrency_test.go`.

## Cambios de datos
Migración `00012` (siguiente libre; 0026 sigue reservado a idempotency-14). Sin palabras de negocio (`kind` de prueba: `test.commitment`, `origin_ref` de prueba: `test.origin/1`).
```sql
ALTER TABLE kernel.entries ADD CONSTRAINT entries_id_space_resource_key UNIQUE (id, space_id, resource_id);
CREATE TABLE kernel.commitments (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  kind text NOT NULL CHECK (char_length(kind) <= 128 AND kind ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'),
  debtor_participant_id uuid NOT NULL, creditor_participant_id uuid NOT NULL,
  resource_id uuid NOT NULL,
  amount numeric NOT NULL CONSTRAINT commitments_amount_check CHECK (amount > 0 AND amount < 1e30), -- NaN and Infinity fail "< 1e30"
  fulfilled_amount numeric NOT NULL DEFAULT 0 CONSTRAINT commitments_fulfilled_within_amount CHECK (fulfilled_amount >= 0 AND fulfilled_amount <= amount),
  starts_at timestamptz NOT NULL DEFAULT now(),
  due_at timestamptz NOT NULL,                      -- criterion 2: no default
  status text NOT NULL CONSTRAINT commitments_status_check CHECK (status IN ('open','fulfilled','canceled','expired')), -- no default
  origin_ref text CHECK (origin_ref IS NULL OR (btrim(origin_ref) <> '' AND char_length(origin_ref) <= 255)), -- opaque to the kernel
  canceled_reason text CHECK (canceled_reason IS NULL OR (btrim(canceled_reason) <> '' AND char_length(canceled_reason) <= 500)),
  closed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),    -- stamped by trigger
  created_by_participant_id uuid NOT NULL,
  CONSTRAINT commitments_due_after_start CHECK (due_at >= starts_at),
  CONSTRAINT commitments_distinct_parties CHECK (debtor_participant_id <> creditor_participant_id),
  CONSTRAINT commitments_closed_coherent CHECK ((status = 'open') = (closed_at IS NULL)),
  CONSTRAINT commitments_canceled_coherent CHECK ((status = 'canceled') = (canceled_reason IS NOT NULL)),
  CONSTRAINT commitments_state_amount_coherent CHECK ((status <> 'open' OR fulfilled_amount < amount) AND (status <> 'fulfilled' OR fulfilled_amount = amount)),
  CONSTRAINT commitments_id_space_key UNIQUE (id, space_id),                    -- target for timers-12
  CONSTRAINT commitments_id_space_resource_key UNIQUE (id, space_id, resource_id),
  CONSTRAINT commitments_debtor_fkey FOREIGN KEY (debtor_participant_id, space_id) REFERENCES kernel.participants (id, space_id),
  CONSTRAINT commitments_creditor_fkey FOREIGN KEY (creditor_participant_id, space_id) REFERENCES kernel.participants (id, space_id),
  CONSTRAINT commitments_created_by_fkey FOREIGN KEY (created_by_participant_id, space_id) REFERENCES kernel.participants (id, space_id),
  CONSTRAINT commitments_resource_fkey FOREIGN KEY (resource_id, space_id) REFERENCES kernel.resources (id, space_id));
CREATE TABLE kernel.fulfillments (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  commitment_id uuid NOT NULL, entry_id uuid NOT NULL, resource_id uuid NOT NULL,
  amount numeric NOT NULL CONSTRAINT fulfillments_amount_check CHECK (amount > 0 AND amount < 1e30),
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT fulfillments_entry_key UNIQUE (entry_id),   -- one entry applies once: race-free by index
  CONSTRAINT fulfillments_commitment_fkey FOREIGN KEY (commitment_id, space_id, resource_id) REFERENCES kernel.commitments (id, space_id, resource_id),
  CONSTRAINT fulfillments_entry_fkey FOREIGN KEY (entry_id, space_id, resource_id) REFERENCES kernel.entries (id, space_id, resource_id));
CREATE INDEX fulfillments_commitment_idx ON kernel.fulfillments (commitment_id);
```
- Las FK compuestas con recurso hacen que el criterio 8 y la mezcla de espacios fallen en la base aunque se borre un disparador (segunda capa).
- RLS `ENABLE` y `FORCE` en ambas; políticas `(SELECT kernel.current_space_id())`: `commitments` SELECT, INSERT y UPDATE (`USING` y `WITH CHECK`); `fulfillments` solo SELECT e INSERT.
- Grants por columnas, sin `ALTER DEFAULT PRIVILEGES`: `commitments` `SELECT`; `INSERT (id, space_id, kind, debtor_participant_id, creditor_participant_id, resource_id, amount, starts_at, due_at, status, origin_ref, created_by_participant_id)`; `UPDATE (fulfilled_amount, canceled_reason)`. `fulfillments` `SELECT`; `INSERT (id, space_id, commitment_id, entry_id, resource_id, amount)`. Sin `status` en `UPDATE`, sin `closed_at`, `created_at`, `DELETE`, `TRUNCATE`, `REFERENCES`, `TRIGGER`. `fulfilled_amount` lleva `UPDATE` porque el disparador que lo mantiene corre con los derechos de quien inserta (sin `SECURITY DEFINER`); ver garantía abajo. `status` va en `INSERT` porque el criterio 2 exige `NOT NULL` sin default; solo admite `open`.
- Funciones: `plpgsql`, `SET search_path = pg_catalog, pg_temp`, objetos `kernel.x`, sin `SECURITY DEFINER`, errores con `ERRCODE` y `CONSTRAINT` como en 00009. Un disparador por tabla y momento (el orden alfabético de disparadores no es un contrato):
  1. `commitments_guard_insert` BEFORE INSERT: `status` distinto de `open` (NULL pasa y lo rechaza `NOT NULL`), `fulfilled_amount <> 0`, `closed_at` o `canceled_reason` no nulos: `restrict_violation`. Fija `created_at := now()`. Lee la escala del recurso (`space_id` igual); `amount <> round(amount, scale)` falla `commitments_amount_scale`; guarda canónico.
  2. `commitments_guard_update` BEFORE UPDATE (el que impide estados incoherentes; todo `restrict_violation`, también al dueño): si `OLD.status <> 'open'` rechaza cualquier `UPDATE` (terminales: `fulfilled`, `canceled`, `expired`); rechaza cambios de cualquier columna salvo `fulfilled_amount`, `canceled_reason` y `status` (`due_at` queda inmutable: timers-12 lo relaja); un `status` escrito por el llamador solo puede ser `open` o `expired`; solo un efecto por sentencia. (i) cambio de `fulfilled_amount`: debe ser igual a `COALESCE(sum(fulfillments.amount),0)` del compromiso (lectura bajo RLS: sin espacio actual no cuadra y falla cerrado); si iguala `amount` fija `status := 'fulfilled'`. (ii) `canceled_reason` de NULL a no vacío: fija `status := 'canceled'`. (iii) `status = 'expired'` explícito: sin otro cambio. En (i), (ii) y (iii) fija `closed_at := now()`; un `closed_at` escrito por el llamador se rechaza.
  3. `fulfillments_guard_insert` BEFORE INSERT, en este orden: (a) `SELECT ... FROM commitments WHERE id, space_id, resource_id FOR NO KEY UPDATE`: sin fila `fulfillments_commitment_fkey`; (b) `status <> 'open'` `fulfillments_commitment_open`; (c) el asiento (`entries` JOIN `accounts`, mismo espacio y recurso): sin fila `fulfillments_entry_fkey`; `entries.amount <= 0` o `accounts.holder_participant_id <> creditor_participant_id` `fulfillments_entry_creditor`; el intercambio con `reverses_exchange_id` no nulo `fulfillments_entry_reversal`; `NEW.amount <> entries.amount` `fulfillments_amount_entry`; (d) `NEW.amount > amount - fulfilled_amount` `fulfillments_within_pending`; (e) `created_at := now()`. El `SELECT ... FOR NO KEY UPDATE` espera al bloqueo de otra transacción y relee la versión confirmada (READ COMMITTED), y cada sentencia de la función ve una instantánea nueva: por eso (d) ve el cumplimiento ajeno ya confirmado.
  4. `fulfillments_apply` AFTER INSERT: `UPDATE kernel.commitments SET fulfilled_amount = (SELECT COALESCE(sum(amount),0) FROM kernel.fulfillments WHERE commitment_id = NEW.commitment_id) WHERE id = NEW.commitment_id AND space_id = NEW.space_id`; el disparador 2 valida y fija estado y `closed_at`.
  5. `commitments_fulfilled_sum_check` y `fulfillments_fulfilled_sum_check`: `CONSTRAINT TRIGGER ... AFTER INSERT OR UPDATE` (en fulfillments solo `INSERT`) `DEFERRABLE INITIALLY DEFERRED`, función `kernel.check_commitment_fulfilled()`: `fulfilled_amount = sum` al confirmar, o `commitments_fulfilled_sum` (segunda capa; ver ataques).
  6. Inmutabilidad: `fulfillments` reutiliza `kernel.reject_ledger_change()` en `BEFORE UPDATE OR DELETE` (fila) y `BEFORE TRUNCATE`; `commitments` usa `kernel.reject_commitment_removal()` en `BEFORE DELETE` y `BEFORE TRUNCATE` (también en cascada desde `spaces`, `participants`, `resources`, `entries`).
  7. `CREATE OR REPLACE FUNCTION kernel.guard_resource_change()`: además de `entries`, `LOCK TABLE kernel.commitments IN SHARE MODE` y rechaza cambiar `scale` o `kind` si existe un compromiso del recurso (si no, bajar la escala dejaría compromisos que el recurso ya no representa).
- `Down`: borra disparadores, funciones y tablas nuevas, el `UNIQUE` de `entries` y restaura el cuerpo previo de `guard_resource_change`.
- Decisiones. (b) Concurrencia: bloqueo de fila en la base (no solo en Go), no un `CHECK` (un `CHECK` no ve filas ajenas) ni un saldo calculado: `fulfilled_amount` es columna derivada (consulta barata, requisito del criterio 5) y la base la mantiene y la valida contra la suma, así que la API solo puede escribir el valor verdadero. (c) Un asiento cumple un solo compromiso, una sola vez y completo (`UNIQUE (entry_id)` y `amount = entries.amount`): sin límite por asiento ni candados consultivos; un pago de 100 para deudas de 60 y 40 se modela con dos asientos del lado del acreedor. Solo cuenta un asiento positivo en una cuenta del acreedor y fuera de una compensación: sin esto, quien escribe compromisos marcaría cobrada cualquier deuda con cualquier asiento. No se exige un asiento del deudor: el ejemplo de `kernel.md` ("cuenta por cobrar del socio") puede estar a nombre del negocio. (d) `entries.commitment_id` NO se añade: fulfillments es la única fuente de verdad, `entries` es inmutable y la columna sería forjable con su grant. (e) y (f) ver Contrato y Riesgos.

## Contrato
```go
// model
type CommitmentStatus string // CommitmentStatusOpen, Fulfilled, Canceled, Expired
type NewCommitment struct{ Kind, DebtorParticipantID, CreditorParticipantID, ResourceID, OriginRef string
    Amount Amount; StartsAt time.Time /* zero = database now() */; DueAt time.Time /* required */ }
type Commitment struct{ ID, SpaceID, Kind, DebtorParticipantID, CreditorParticipantID, ResourceID, OriginRef, CanceledReason, CreatedByParticipantID string
    Amount, FulfilledAmount Amount; Status CommitmentStatus; StartsAt, DueAt, CreatedAt time.Time /* UTC */
    ClosedAt time.Time /* zero while open */; Fulfillments []Fulfillment /* only GetCommitment */ }
type Fulfillment struct{ ID, SpaceID, CommitmentID, EntryID, ResourceID string; Amount Amount; CreatedAt time.Time }
func (c NewCommitment) Validate() error
// state: Append* siguen a AppendExchange (reciben la transacción, no abren, confirman ni revierten; sin savepoints; el espacio sale de kernel.current_space_id())
func AppendCommitment(ctx context.Context, tx *sql.Tx, actor model.Actor, in model.NewCommitment) (model.Commitment, error)
func AppendFulfillment(ctx context.Context, tx *sql.Tx, actor model.Actor, commitmentID, entryID string) (model.Fulfillment, model.Commitment, error)
func AppendCancelCommitment(ctx context.Context, tx *sql.Tx, actor model.Actor, commitmentID, reason string) (model.Commitment, error)
func CreateCommitment(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID string, in model.NewCommitment) (model.Commitment, error)
func FulfillCommitment(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID, commitmentID, entryID string) (model.Fulfillment, model.Commitment, error)
func CancelCommitment(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID, commitmentID, reason string) (model.Commitment, error)
func GetCommitment(ctx context.Context, pool *sql.DB, spaceID, commitmentID string) (model.Commitment, error)
```
- Validación sin escribir (`invalid_input`): `kind` con el patrón de tipos de evento, UUID no nulos, deudor distinto del acreedor, `Amount.Sign() > 0`, `DueAt` no cero, `DueAt >= StartsAt` si `StartsAt` no es cero, `OriginRef` de 1 a 255 sin blancos, motivo de cancelación de 1 a 500 sin blancos. Actor: `validateExchangeActor` (solo `system` con participante; `permission_denied`); sin espacio actual `rule_violation` antes de escribir. UUID v7 con `uuid.NewV7`; `created_by = actor.ParticipantID`, nunca un valor del llamador; `.UTC()` al leer.
- `AppendFulfillment` no recibe monto: inserta con `INSERT ... SELECT e.resource_id, e.amount FROM kernel.entries e WHERE e.id = $entry` (cero filas, asiento ajeno o inexistente: `rule_violation`), con lectura previa del compromiso (inexistente en este espacio: `not_found`), y relee el compromiso tras el disparador. `AppendCancelCommitment`: `UPDATE ... SET canceled_reason = $2 WHERE id = $1 AND status = 'open' RETURNING ...`; sin fila, `SELECT status`: inexistente `not_found`, no abierto `already_done` (el `WHERE` se reevalúa tras esperar, así que cancelar contra cumplir tiene un ganador).
- Traducción por `ConstraintName`, luego código: `commitments_*_fkey`, `fulfillments_commitment_fkey`, `fulfillments_entry_fkey`, `fulfillments_commitment_open`, `fulfillments_within_pending`, `fulfillments_entry_creditor`, `fulfillments_entry_reversal`, `fulfillments_amount_entry`, `fulfillments_entry_key`: `rule_violation`. `commitments_amount_scale`, 23514, 22P02: `invalid_input`. `23502` se mapea por `pgErr.ColumnName` (seguimiento 37): `space_id` es `rule_violation`, otra columna `invalid_input`. Los errores de `Commit` se traducen. `restrict_violation` de los disparadores no se tipa (alcanzarlo es un error del código). `40001`/`40P01` no se traducen: el llamador reintenta.
- Estados: `open` a `fulfilled` (el último cumplimiento llega al monto), `open` a `canceled` (motivo), `open` a `expired` (gancho de timers-12). Todo lo demás se rechaza. Permisos `kernel.commitment.write` y `.read` los aplica execution-13 (seguimiento 49).

## Pruebas requeridas
Primero y en rojo, con PostgreSQL real (`dbtest.Database`, `dbtest.APIPool`, `seedSpaces`); fallan, no se omiten. Orden: 1) `model/commitment_test.go`; 2) `db/commitments_schema_test.go` y `commitments_security_test.go`; 3) `state/commitment_test.go` y `commitment_concurrency_test.go`. SQL crudo con `BEGIN ... COMMIT` y comprobando el error del `COMMIT`.
- C1: `TestCommitmentCreatedOpenWithZeroFulfilled` (state; estado `open`, `0`, `CreatedAt` UTC) y `TestAPIInsertCommitmentStoresDerivedColumns` (db).
- C2: `TestCommitmentWithoutDueAtOrStatusRejected` (db; 23502 con el dueño y con la API; `due_at` y `status` sin default en el catálogo).
- C3: `TestNewCommitmentValidateRejectsBadInput` (model: monto 0, negativo, vencimiento anterior, mismas partes, sin vencimiento), `TestCreateCommitmentInvalidInput` (state), `TestCommitmentChecksRejectedByDatabase` (db: `amount <= 0` y `due_at < starts_at` dan 23514).
- C4: `TestFulfilledAmountCannotExceedAmount` (db; en base desechable con `ALTER TABLE ... DISABLE TRIGGER commitments_guard_update` el `UPDATE` da 23514 `commitments_fulfilled_within_amount`; el rol de la API y el dueño con el disparador activo reciben `restrict_violation`, nunca fijan el valor).
- C5: `TestFulfillPartialKeepsOpen` (state: asiento de 40.00, un `fulfillments`, `40`, `open`).
- C6: `TestFulfillRemainderClosesCommitment` (asientos de 40.00 y 60.00; `fulfilled`, `closed_at` entre el instante previo y posterior, `fulfillments` suma 100).
- C7: `TestFulfillBeyondPendingIsRuleViolationAndWritesNothing` (asiento de 70.00 sobre 60 pendientes; cero filas nuevas, `fulfilled_amount` igual), `TestConcurrentFulfillmentsOnlyOneWins` (dos goroutines con asientos de 60.00 sobre 100: un ganador, otro `rule_violation`, `fulfilled_amount = 60`) y `TestFulfillWaitsForRowLock` (la transacción 1 cumple sin confirmar; la 2 espera con `wait_event_type = 'Lock'` en `pg_stat_activity`; al confirmar la 1, la 2 recibe `rule_violation`; sin `FOR NO KEY UPDATE` la 2 no espera).
- C8: `TestFulfillWithEntryOfOtherResourceIsRuleViolation`, `TestFulfillWithEntryOfOtherSpaceIsRuleViolation` (asiento real de B desde A), `TestFulfillWithDebitEntryOrNonCreditorEntryIsRuleViolation`, `TestFulfillWithReversalEntryIsRuleViolation`, `TestEntryAppliesOnlyOnce` (misma o distinta deuda; concurrente: un ganador), `TestFulfillmentAmountMustEqualEntry` (db).
- C9: `TestCancelOpenCommitment` (`canceled`, `closed_at` y motivo; cancelar uno parcialmente cumplido conserva `fulfilled_amount`), `TestCancelWithoutReasonIsInvalidInput`, `TestCancelClosedCommitmentIsAlreadyDone` (los tres terminales; `expired` fijado como dueño), `TestCancelRacesWithFulfill` (un ganador; el perdedor `already_done` o `rule_violation`).
- C10: `TestFulfillNonOpenCommitmentIsRuleViolation` (los tres estados, incluido `expired` fijado por el dueño; así timers-12 criterio 9 ya queda cubierto).
- C11: `TestFulfillmentsAreAppendOnly` (db; API: `UPDATE`, `DELETE`, `TRUNCATE` dan 42501; administrador: `restrict_violation`, incluido `TRUNCATE ... CASCADE` desde `commitments`, `entries`, `spaces`; con `GRANT UPDATE, DELETE` concedido por error los disparadores siguen rechazando) y `TestCommitmentsCannotBeDeleted`.
- C12: `TestCommitmentsIsolationBetweenSpaces` (B lee 0 filas; `GetCommitment` de A desde B es `not_found`; B inserta con `space_id` de A: 42501; `UPDATE` de B sobre uno de A afecta 0 filas), `TestCommitmentsForcedRLSAndGrants` (`ENABLE`, `FORCE`, solo las políticas previstas, `column_privileges` exactos, conteo de columnas del grant).
- Ataques (rol de la API): `TestAPICannotForgeStatusOrFulfilledAmount` (`INSERT` con `status = 'fulfilled'`: `restrict_violation`; con `fulfilled_amount`, `closed_at`, `created_at`: 42501; `UPDATE ... SET fulfilled_amount = amount` sin cumplimientos: `restrict_violation`, sigue `open`), `TestAPICannotSetStatusOrExpire` (42501), `TestAPICannotChangeIdentityColumns` (`amount`, `due_at`, partes: 42501), `TestTerminalStatesAreFinal` (dueño: reabrir, cambiar motivo o `closed_at` falla), `TestCommitmentOfOtherSpaceReferencesRejected` (deudor, acreedor, recurso y actor de B en A: 23503), `TestFulfillmentDeferredSumCheck` (disparador de guarda deshabilitado en base desechable: el `COMMIT` falla `commitments_fulfilled_sum`), `TestOnConflictCannotBypassGuards` (`DO UPDATE` rechazado; `DO NOTHING` no escribe), `TestCopyFromStillFiresTriggers`, `TestSessionReplicationRoleDeniedToAPI` (42501), `TestSpaceSwitchBeforeCommitFailsClosed`, `TestCommitmentFunctionsPinSearchPathAndNotSecurityDefiner` (catálogo), `TestResourceScaleAndKindFrozenOnceCommitmentsExist`, `TestAmountBeyondResourceScaleIsInvalidInput` (`10.001` en escala 2; `10.100` se guarda `10.10`).
- Resto: `TestAmountColumnsAreNumeric` actualizado; `kernel/architecture_test.go` y `adr_test.go` en verde (sin vocabulario de negocio, tampoco en SQL ni comentarios).
- Antes del PR el desarrollador muta una línea a la vez y comprueba que alguna prueba falla: quitar `FOR NO KEY UPDATE`; quitar la comparación con la suma en el disparador 2; quitar la derivación de `status`; `<=` por `<` en `fulfillments_within_pending`; quitar `UNIQUE (entry_id)`; quitar `resource_id` de las FK; quitar la comprobación del acreedor o de la compensación; quitar `closed_at := now()`; quitar el disparador de `TRUNCATE`; quitar la cláusula de compromisos en `guard_resource_change`; añadir `UPDATE (status)` o `INSERT (closed_at)` al grant.

## Riesgos técnicos y mitigación
- **Contradicciones y decisiones (recomendación).** 1) Criterio 2 exige `NOT NULL` en `status` sin default, pero la API no debe fijarlo: `status` se inserta solo como `open` (el disparador rechaza otro) y no se actualiza nunca por la API; cambia por derivación. 2) Criterio 4 dice `CHECK`, pero el disparador responde antes con `restrict_violation`; el `CHECK` se prueba con el disparador deshabilitado. 3) La historia pide solo mismo recurso y espacio para el asiento; se añaden: positivo, de una cuenta del acreedor, no de una compensación, aplicado una vez y completo (el responsable puede objetar; alternativa: cumplimiento parcial por asiento con tope y candado consultivo, no hecha porque añade carreras y la historia no lo exige). 4) Se añaden `created_by_participant_id` (regla Permiso de `kernel.md`: toda fila escrita tiene actor), `created_at` y `space_id` y `resource_id` en `fulfillments`, ausentes del ER. 5) Deudor distinto del acreedor, `amount < 1e30`, escala por recurso y `due_at` inmutable: reglas nuevas derivadas. 6) `commitment_id` en `entries` (backlog línea 296, nota de exchanges-08) se descarta; el modelo vigente ya no lo trae. 7) `leyes.md` cita tres estados (`abierto`, `cumplido`, `cancelado`) y el modelo cuatro; se sigue el modelo (pregunta 6 del brief).
- **Gancho para timers-12 (ley de compromiso y movimiento).** Aquí NO hay timers, expiración, reprogramación de `due_at`, eventos ni outbox. Queda `expired` en el `CHECK`, terminal y con `closed_at` por el kernel; el disparador acepta `open` a `expired` escrito por el dueño, y la API no tiene grant de `status`. timers-12 debe: añadir `UPDATE (due_at)` y relajar la guarda en una migración nueva, concederle al proceso del kernel una vía acotada para `expired` (el disparador no ve el reloj inyectable, no puede comprobar `due_at <= reloj`; seguimiento 41/45), y extender `AppendCommitment` con el timer en la misma transacción. `AppendCommitment` devuelve `ID` y `DueAt` para eso.
- **`fulfilled_amount` con grant de `UPDATE`.** Imprescindible porque los disparadores usan derechos del invocador; el valor solo puede igualar la suma de cumplimientos (guarda inmediata y comprobación diferida), así que no se puede falsificar; un superusuario o dueño puede deshabilitar disparadores (límite de ADR-0028; `CHECK` y `UNIQUE` siguen).
- **Un compromiso cumplido no se reabre si se compensa el pago** (compensar el intercambio del asiento no toca `fulfillments`). Seguimiento 51, decidir en un ADR.
- **Seguimientos nuevos.** 51 compensación de un asiento aplicado; 52 cumplimiento parcial de un asiento; 53 acotar la vía de `expired` (timers-12); 54 participantes archivados como partes (como el 18); 55 `due_at` en el pasado se admite (reloj inyectable) y expirará en el primer disparo.
- **`REPEATABLE READ`.** `FOR NO KEY UPDATE` daría 40001; hoy todo es `READ COMMITTED`; no se traduce (seguimiento 33). Dos compromisos cumplidos en una transacción en orden opuesto por otra pueden dar 40P01; el llamador reintenta.
- **Vector no cubierto por diseño:** el actor lo afirma la aplicación (seguimientos 29, 46).

## Fuera de alcance
Timers, vencimiento, reprogramación y eventos `kernel.commitment.*` (timers-12 y execution-13), outbox, permisos `kernel.commitment.*` (execution-13), idempotencia de creación y cumplimiento (idempotency-14), `entries.commitment_id`, cumplimiento parcial de un asiento, reapertura por compensación, mora e intereses, `origin_ref` con significado de negocio, listados y paginación, pruebas de `kernel/laws/` (laws-15 reutilizará estas).
