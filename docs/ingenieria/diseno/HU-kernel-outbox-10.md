# Diseño: HU-kernel-outbox-10

## Resumen
Se crea `kernel.outbox`, una cola por evento y suscriptor con RLS forzada, grants por columnas y un disparador que solo admite las transiciones legítimas (reclamar, enviar, fallar, morir). En Go, `kernel/state` escribe la fila dentro de la transacción del evento, reclama lotes con `FOR UPDATE SKIP LOCKED` más un arrendamiento (`claim_token`, `claimed_until`) y marca `sent` o `failed` con el reloj inyectable. Estado: lista, sin bloqueos; requiere ADR-0031 antes del código (regla 12).

## Tipo de trabajo
`backend`: SQL y Go del kernel, sin contrato público ni app.

## Módulos a tocar
- `docs/02-arquitectura/modelo-de-datos/kernel.md` (sección 4): OUTBOX gana `space_id`, `claim_token`, `claimed_until`, `created_at`; clave `(event_seq, subscriber)`; explicar arrendamiento y que el outbox no es append-only (es estado de trabajo).
- `docs/product/backlog/nucleo-del-kernel.md`: estado y decisiones de abajo. `docs/ingenieria/sprint-01/seguimientos.md`: añadir los seguimientos de Riesgos.
- `kernel/state/event.go` no cambia: `AppendEvent` ignora suscriptores (menor acoplamiento).
- `kernel/db/events_schema_test.go`: verificar que el `UNIQUE (seq, space_id)` nuevo no rompe sus aserciones de índices.

## Archivos nuevos
- `docs/05-decisiones/0031-outbox-reclamo-y-arrendamiento.md` (antes del código). Decide: (a) reclamo con arrendamiento de 5 min, entrega al menos una vez, el consumidor deduplica por `event.id`; (b) una transacción reclama un solo espacio (ningún rol con `BYPASSRLS`, ADR-0027); el despacho entre espacios es de B3 y exigirá otro ADR (lista de espacios activos, equidad); (c) un reclamo vencido cuenta como intento fallido para acotar mensajes venenosos; (d) límite de 5 intentos y espera `30 s, 2 min, 10 min, 1 h`, valores de partida que B3 puede cambiar con migración.
- `kernel/db/migrations/00010_outbox.sql`: esquema abajo.
- `kernel/model/outbox.go`: `Subscriber` (+`Valid`), `OutboxStatus`, `OutboxRow`, `OutboxMaxAttempts = 5`, `OutboxLease = 5 * time.Minute`, `OutboxBackoff(attempts int) time.Duration`.
- `kernel/state/outbox.go`: API Go y traducción de errores PG.
- Pruebas: `kernel/model/outbox_test.go`, `kernel/db/outbox_schema_test.go`, `kernel/db/outbox_security_test.go`, `kernel/state/outbox_test.go`, `kernel/state/outbox_concurrency_test.go`.

## Cambios de datos
Migración `00010_outbox.sql` (siguiente libre tras 00009):
```sql
-- +goose Up
ALTER TABLE kernel.events ADD CONSTRAINT events_seq_space_key UNIQUE (seq, space_id);
CREATE TABLE kernel.outbox (
  event_seq bigint NOT NULL,
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  subscriber text NOT NULL CONSTRAINT outbox_subscriber_check CHECK (subscriber IN ('webhooks','n8n','ai','internal')),
  status text NOT NULL DEFAULT 'pending' CONSTRAINT outbox_status_check CHECK (status IN ('pending','sent','failed','dead')),
  attempts integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL,
  last_error text CHECK (last_error IS NULL OR (btrim(last_error) <> '' AND char_length(last_error) <= 2000)),
  delivered_at timestamptz,
  claim_token uuid,
  claimed_until timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (event_seq, subscriber),
  CONSTRAINT outbox_event_fkey FOREIGN KEY (event_seq, space_id) REFERENCES kernel.events (seq, space_id),
  CONSTRAINT outbox_claim_check CHECK ((claim_token IS NULL) = (claimed_until IS NULL)),
  CONSTRAINT outbox_state_check CHECK (   -- the limit of 5 lives here and in OutboxMaxAttempts
    (status = 'pending' AND attempts = 0 AND last_error IS NULL AND delivered_at IS NULL)
    OR (status = 'failed' AND attempts BETWEEN 1 AND 4 AND last_error IS NOT NULL AND delivered_at IS NULL)
    OR (status = 'sent' AND attempts BETWEEN 0 AND 4 AND delivered_at IS NOT NULL AND claim_token IS NULL)
    OR (status = 'dead' AND attempts = 5 AND last_error IS NOT NULL AND delivered_at IS NULL AND claim_token IS NULL))
);
CREATE INDEX outbox_due_idx ON kernel.outbox (space_id, subscriber, next_attempt_at, event_seq)
  WHERE status IN ('pending','failed') AND claim_token IS NULL;
CREATE INDEX outbox_lease_idx ON kernel.outbox (space_id, subscriber, claimed_until) WHERE claim_token IS NOT NULL;
ALTER TABLE kernel.outbox ENABLE ROW LEVEL SECURITY;  ALTER TABLE kernel.outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY outbox_select ON kernel.outbox FOR SELECT USING (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY outbox_insert ON kernel.outbox FOR INSERT WITH CHECK (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY outbox_update ON kernel.outbox FOR UPDATE USING (space_id = (SELECT kernel.current_space_id()))
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));          -- no DELETE policy
GRANT SELECT ON kernel.outbox TO nexusthrive_api;
GRANT INSERT (event_seq, space_id, subscriber, next_attempt_at) ON kernel.outbox TO nexusthrive_api;
GRANT UPDATE (status, attempts, next_attempt_at, last_error, delivered_at, claim_token, claimed_until)
  ON kernel.outbox TO nexusthrive_api;   -- no DELETE, TRUNCATE, REFERENCES or TRIGGER
```
Disparadores (`LANGUAGE plpgsql SET search_path = pg_catalog, pg_temp`, sin `SECURITY DEFINER`):
- `kernel.reject_outbox_removal()` en `BEFORE DELETE FOR EACH ROW` y `BEFORE TRUNCATE FOR EACH STATEMENT` (`restrict_violation`; dispara también por `TRUNCATE ... CASCADE`). La retención llegará con su propia migración (B6).
- `kernel.guard_outbox_change()` en `BEFORE UPDATE FOR EACH ROW`, para todos los roles incluido el dueño:
  1. `(event_seq, space_id, subscriber, created_at)` no cambia.
  2. `OLD.status IN ('sent','dead')`: se rechaza todo (terminales; sin retroceso).
  3. `OLD.claim_token IS NULL` (libre): solo se permite reclamar: `NEW.claim_token` no nulo, y `status`, `attempts`, `next_attempt_at`, `last_error`, `delivered_at` iguales.
  4. Reclamada: `NEW.claim_token IS NULL` siempre, y solo dos salidas: `sent` (con `attempts` y `last_error` iguales) o `failed`/`dead` (`attempts = OLD.attempts + 1` exacto y `next_attempt_at >= OLD.next_attempt_at`). Cualquier otra, incluida volver a reclamar una fila ya reclamada, es `restrict_violation`.

`outbox_state_check` liga `dead` a `attempts = 5`, así que no se puede saltar ni evadir el límite. `Down`: tabla, ambas funciones y la restricción de `events`. Límite honesto: el disparador valida la forma, no el tiempo, porque la base no ve el reloj inyectable; `delivered_at` y `next_attempt_at` los fija el kernel en Go con ese reloj.

## Contrato
```go
// model
type Subscriber string // "webhooks", "n8n", "ai", "internal"; (Subscriber).Valid() bool
type OutboxRow struct { EventSeq int64; SpaceID string; Subscriber Subscriber; Status OutboxStatus
    Attempts int; NextAttemptAt time.Time; LastError string; DeliveredAt, ClaimedUntil *time.Time; ClaimToken string }
// state
func EnqueueOutbox(ctx context.Context, tx *sql.Tx, clock model.Clock, eventSeq int64, s model.Subscriber) error
func AppendEventAndEnqueue(ctx context.Context, tx *sql.Tx, clock model.Clock, in model.NewEvent, subs ...model.Subscriber) (model.Event, error)
func ClaimOutbox(ctx context.Context, pool *sql.DB, clock model.Clock, spaceID string, s model.Subscriber, limit int) ([]model.OutboxRow, error)
func ClaimOutboxTx(ctx context.Context, tx *sql.Tx, clock model.Clock, s model.Subscriber, limit int) ([]model.OutboxRow, error)
func MarkOutboxSent(ctx context.Context, pool *sql.DB, clock model.Clock, spaceID string, row model.OutboxRow) error
func MarkOutboxFailed(ctx context.Context, pool *sql.DB, clock model.Clock, spaceID string, row model.OutboxRow, cause string) error
```
- **Integración.** `AppendEvent` queda intacto. `EnqueueOutbox` es la escritura de una fila; `AppendEventAndEnqueue` valida suscriptores (inválido o repetido: `invalid_input`) antes de escribir, llama `AppendEvent` y luego `EnqueueOutbox` por suscriptor. Es la interfaz que consumen timers-12 y execution-13, que además comprobarán `kernel.outbox.claim` (permissions-06 aún no está fusionada: `ClaimOutbox` no recibe actor en M1).
- **Escritura.** Nunca abre, confirma ni revierte. `next_attempt_at = clock.Now().UTC().Truncate(time.Microsecond)`; `status`, `attempts` y `created_at` los pone el `DEFAULT`, no el llamador. `space_id` sale de `kernel.current_space_id()`. Errores: `23514` a `invalid_input`; `23503` en `outbox_event_fkey` (evento inexistente o de otro espacio; mismo mensaje, no revela existencia), `23505` y `23502` con `ColumnName = space_id` a `rule_violation` (mapear por columna, seguimiento 37).
- **Reclamo.** `limit` entre 1 y 1000 y suscriptor válido, si no `invalid_input`. En una transacción de un solo espacio, en este orden: (1) vencer reclamos caducados: filas con `claim_token` no nulo y `claimed_until <= now` (`FOR UPDATE SKIP LOCKED`, máx. 1000) pasan por la misma transición de fallo con `last_error = 'claim expired'` (intento +1, espera, `dead` al quinto); (2) `SELECT ... WHERE subscriber = $1 AND status IN ('pending','failed') AND claim_token IS NULL AND next_attempt_at <= $now ORDER BY next_attempt_at, event_seq LIMIT $n FOR UPDATE SKIP LOCKED` y `UPDATE` de esas filas con `claim_token` (UUID v7 nuevo por llamada) y `claimed_until = now + OutboxLease`. Devuelve las filas ordenadas. Al confirmar se sueltan los bloqueos; el arrendamiento es lo que impide el doble reclamo.
- **Marcas.** `UPDATE ... WHERE event_seq, subscriber, claim_token = row.ClaimToken AND status IN ('pending','failed')`. Con 0 filas se diagnostica: no existe o de otro espacio, `not_found`; `sent` o `dead`, `already_done`; token distinto o reclamo vencido, `rule_violation` ("claim lost"). Enviar fija `delivered_at = clock.Now()`. Fallar: `cause` en blanco, `invalid_input`; se recorta a 1000 caracteres; `attempts + 1`; con 5 pasa a `dead`, si no `failed` con `next_attempt_at = now + OutboxBackoff(attempts)`.
- **Semántica.** Entrega al menos una vez: un consumidor que entrega y muere antes de marcar provoca reentrega; deduplica por `event.id`.

## Pruebas requeridas
Orden: todas primero y en rojo; fallan, no se omiten; `dbtest.Database` y `dbtest.APIPool`; reloj con `fixedClock` de `role_test.go`, nunca tiempo real. Fallos de ejecución de `go test ./kernel/...` se corrigen en la implementación, no en la prueba.
1. `model/outbox_test.go`: `TestSubscriberValid` (los 4 válidos; `""`, `Internal`, `internal `, `webhook`, inyección SQL), `TestOutboxBackoffSchedule` (30 s, 2 min, 10 min, 1 h; `OutboxMaxAttempts == 5`).
2. Criterio 1, `state/outbox_test.go`: `TestEnqueueOutboxWritesPendingRow` (`pending`, `attempts 0`, `next_attempt_at` exacto al reloj, sin claim, espacio correcto).
3. Criterio 2: `TestAppendEventAndEnqueueRollbackLeavesNothing` (rollback: 0 eventos y 0 filas, leído con admin; con commit, ambos).
4. Criterio 3: `TestEnqueueOutboxRejectsUnknownSubscriber` (tabla; `invalid_input`, 0 filas, transacción aún usable); `TestOutboxSubscriberCheckConstraint` (db, SQL con administrador). Además `TestEnqueueOutboxRequiresEventOfSameSpace` (evento de A desde B, duplicado y sin espacio: `rule_violation`).
5. Criterio 4, `outbox_concurrency_test.go`: `TestClaimNeverDeliversTwiceConcurrently` (20 repeticiones, 10 filas nuevas, dos reclamantes de lote 10 con barrera; intersección vacía, unión de 10); `TestClaimSkipsLockedRowsWithoutBlocking` (A reclama en transacción abierta, B termina en menos de 2 s con 0 filas: falla sin `SKIP LOCKED`); `TestClaimedRowsAreNotClaimedAgain` (reclamo confirmado y segundo reclamo secuencial devuelve 0: falla sin el filtro de `claim_token`).
6. Criterio 5: `TestMarkSentFixesDeliveredAt`; `TestMarkSentTwiceIsAlreadyDone`; `TestMarkWithWrongTokenIsRuleViolation`; `TestMarkUnknownRowIsNotFound`; `TestMarkSentOnDeadRowIsAlreadyDone`.
7. Criterio 6: `TestMarkFailedIncrementsAttemptsAndSpacesRetries` (intentos 1 a 4: `failed`, error guardado, espera por la tabla); `TestFifthFailureMakesRowDead` (límite exacto: la 4.ª es `failed`, la 5.ª `dead`; una `dead` no se reclama aunque venza); `TestExpiredClaimCountsAsFailedAttempt` (reloj +5 min +1 s: reclamo vence, `attempts 1`; reclamable tras la espera; el antiguo titular recibe `rule_violation`); `TestExpiredClaimsReachDead`.
8. Criterio 7: `TestClaimSkipsRowsNotYetDue` (`next_attempt_at = ahora + 1 µs` no sale; `= ahora` sí; reloj que avanza lo libera).
9. Criterio 8: `TestClaimIgnoresOtherSpaces`, `TestMarkCannotTouchOtherSpace` (B con el token válido de A: `not_found`, fila intacta). Validaciones: `limit` 0 y 1001, suscriptor inválido.
10. `db/outbox_security_test.go` (SQL crudo): `TestOutboxForcedRLSAndPolicies` (ENABLE, FORCE, solo políticas SELECT, INSERT, UPDATE; grants exactos por `column_privileges`); `TestOutboxAPIInsertCannotSetKernelColumns` (`status`, `attempts`, `claim_token`, `delivered_at`, `created_at`: 42501); `TestOutboxTransitionsRejected` (API y administrador, `restrict_violation`: retroceder `sent` o `dead`, bajar `attempts`, saltar a `+2`, `dead` con `attempts 1`, `next_attempt_at` hacia atrás, fijar `delivered_at` sin reclamo, robar una fila reclamada, `last_error` al reclamar); `TestOutboxIdentityColumnsImmutable` (API 42501, administrador `restrict_violation`); `TestOutboxNoDeleteNoTruncate` (API 42501; administrador `restrict_violation`, incluido `TRUNCATE kernel.events CASCADE` y `kernel.spaces CASCADE`); `TestOutboxOnConflictCannotRewrite` (`ON CONFLICT DO UPDATE` sobre `sent` y libre); `TestOutboxCopyRespectsColumnGrants`; `TestOutboxAPICannotDisableTriggersOrChangeRole` (`ALTER TABLE ... DISABLE TRIGGER`, `SET session_replication_role`, `SET ROLE` al dueño: 42501); `TestOutboxCompositeForeignKey` (evento de A con `space_id` de B: 23503); `TestOutboxUpdateOtherSpaceAffectsZeroRows`; `TestOutboxStateCheckConstraints`.
11. Arquitectura: `adr_test.go` y la prueba de vocabulario siguen verdes (sin términos de negocio; eventos `kernel.test.recorded`).

## Riesgos técnicos y mitigación
- **Ataques considerados.** Insertar filas ya `sent` o con intentos falsos (grant por columnas); retroceder o reabrir filas terminales (disparador); saltarse el límite de 5 (CHECK y disparador); robar una fila reclamada (transición de reclamada solo a salidas); marcar con un token ajeno (`WHERE claim_token`); cruzar espacios con FK compuesta, RLS y `WITH CHECK`; borrar o truncar, también con `CASCADE` (sin grant y disparador); `OVERRIDING SYSTEM VALUE` (no hay columna identidad); `COPY` y `ON CONFLICT` (mismos grants y disparador); `SET ROLE`, `ALTER TABLE`, `session_replication_role` (no es dueño ni superusuario); función suplantada (`search_path` fijo, sin `SECURITY DEFINER`).
- **Límites aceptados.** Un superusuario deliberado deshabilita disparadores (B6 audita DDL). El mismo rol de la API que reclama puede forjar `delivered_at` o un reclamo anticipado: es el proceso del kernel, no un cliente; B3 no debe exponer estas funciones a clientes. Reabrir una fila `dead` no tiene vía (la consola de B4 la traerá con migración).
- **Proceso que muere.** El arrendamiento vence a los 5 min; la fila se cuenta como fallo y vuelve a la cola con espera; si el consumidor es venenoso llega a `dead` en 5 vueltas. Costo: la entrega pudo ocurrir (al menos una vez).
- **Un espacio por transacción.** Sin rol con `BYPASSRLS`, un despachador de varios espacios necesita conocer la lista de espacios y rotarlos; es de B3 (ADR-0031 lo declara). Un espacio suspendido no se pausa en M1 (flujos.md); lo decide execution-13 o B4.
- **`READ COMMITTED`.** El reclamo vuelve a evaluar el `WHERE` tras bloquear, por eso no entrega filas ya reclamadas; no usar `REPEATABLE READ` (seguimiento 33, error 40001 sin traducir).
- **Reloj.** Go trunca a microsegundos para que `<=` sea exacto; el reloj de la base solo estampa `created_at`.
- **Contradicciones historia y documentación (recomendación).**
  1. "Superar 5 intentos" frente a "límite de 5": recomiendo `dead` en el quinto fallo (`attempts = 5`); `flujos.md` habla de "hasta 8" para el flujo general: lo corrige B3 al construir el despacho.
  2. Los criterios 4 a 6 hablan de fila "reclamada", y el modelo no tiene columnas de reclamo: se añaden `claim_token` y `claimed_until` (sin ellas el reclamo confirmado se repite).
  3. El criterio 1 sugiere que el evento deja la fila: recomiendo no acoplar `AppendEvent`, sino `AppendEventAndEnqueue`.
  4. La historia cita `kernel.outbox.claim` pero depende solo de events-09: el permiso se aplica en execution-13.
  5. La ley de memoria cubre eventos y asientos, no el outbox: es estado mutable con transiciones acotadas; documentarlo en `kernel.md`.
- Seguimientos nuevos: `migrate` Up-Down-Up de 00010 (como seguimiento 36); consumidor de B3 deduplica por `event.id`.

## Fuera de alcance
Consumidor y bucle periódico de reclamo, `scheduler_runs`, entregas firmadas, `webhook_*`, n8n, MCP, despacho entre espacios, pausa por espacio suspendido, reabrir filas `dead`, retención o purga, permiso `kernel.outbox.claim` y actor (execution-13), eventos de timers (timers-12).
