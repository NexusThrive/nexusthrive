# Diseño: HU-kernel-events-09

## Resumen
Se crea `kernel.events`, un registro de solo agregar con `seq` global, protegido por RLS, grants mínimos y un disparador que rechaza `UPDATE`, `DELETE` y `TRUNCATE` incluso al dueño. En Go, `kernel/state` ofrece `AppendEvent` (recibe la transacción, la usará HU-kernel-execution-13) y lecturas ordenadas por `seq`.

## Tipo de trabajo
`backend`: solo SQL y Go del kernel; no hay contrato público ni app. Estado: lista, sin bloqueos.

## Módulos a tocar
- `kernel/model/space.go`: `Actor` gana `ParticipantID string` y se añaden `ActorPerson`, `ActorAgent`, `ActorAPIKey` y `ActorKind.Valid()`. Los literales `Actor{Kind: ...}` existentes siguen compilando.
- `docs/02-arquitectura/modelo-de-datos/kernel.md`: sección 4 y Reglas (formato de `type`, trigger, orden de lectura, FK compuesta del actor).
- `docs/product/backlog/nucleo-del-kernel.md`: estado de la historia y decisiones de abajo.
- `docs/05-decisiones/0028-orden-de-eventos.md` (nuevo, antes del código; regla 12): garantías de `seq` y deber del consumidor (sección Riesgos). Confirmar que 0028 está libre.

## Archivos nuevos
- `kernel/db/migrations/00006_events.sql`: tabla, índices, RLS, trigger y grants (esquema abajo).
- `kernel/model/event.go`: `EventType`, `NewEvent`, `Event`, `(NewEvent).Validate()` pura, sin base de datos.
- `kernel/state/event.go`: `AppendEvent`, `RecordEvent`, `ReadEvents`, `ReadEventsTx`, traducción de errores PG.
- Pruebas: `kernel/model/event_test.go`, `kernel/db/events_schema_test.go`, `kernel/state/event_test.go`.

## Cambios de datos
Migración `00006_events.sql` (la siguiente libre tras 00005):
```sql
-- +goose Up
-- Composite key target so an event's actor must live in the event's space.
ALTER TABLE kernel.participants ADD CONSTRAINT participants_id_space_key UNIQUE (id, space_id);
CREATE TABLE kernel.events (
  seq bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  id uuid NOT NULL UNIQUE CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  type text NOT NULL CHECK (char_length(type) <= 128
    AND type ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'),
  version integer NOT NULL CHECK (version >= 1),
  subject_ref text CHECK (subject_ref IS NULL OR btrim(subject_ref) <> ''),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  actor_participant_id uuid,
  actor_kind text NOT NULL CHECK (actor_kind IN ('person','agent','api_key','system')),
  request_id text CHECK (request_id IS NULL OR btrim(request_id) <> ''),
  occurred_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT events_actor_required CHECK (actor_participant_id IS NOT NULL OR actor_kind = 'system'),
  CONSTRAINT events_actor_fkey FOREIGN KEY (actor_participant_id, space_id)
    REFERENCES kernel.participants (id, space_id)
);
CREATE INDEX events_space_seq_idx ON kernel.events (space_id, seq);
```
Luego `ENABLE` y `FORCE` RLS, y dos políticas por comando (sin política de `UPDATE` ni `DELETE`):
`events_select FOR SELECT USING (space_id = (SELECT kernel.current_space_id()))` y `events_insert FOR INSERT WITH CHECK (space_id = (SELECT kernel.current_space_id()))`.
Grants: `GRANT SELECT, INSERT ON kernel.events TO nexusthrive_api;` (la identidad no requiere grant sobre la secuencia). Sin `UPDATE`, `DELETE` ni `TRUNCATE`.

Inmutabilidad en dos capas, justificada porque los grants protegen solo al rol de la API: el dueño de la tabla y el administrador tienen `UPDATE`/`DELETE` por defecto, y un grant se puede otorgar por error en una migración futura. Por eso la migración añade:
```sql
CREATE FUNCTION kernel.reject_event_change() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp AS $$
BEGIN RAISE EXCEPTION 'events are append-only' USING ERRCODE = 'restrict_violation'; END $$;
CREATE TRIGGER events_no_row_change BEFORE UPDATE OR DELETE ON kernel.events
  FOR EACH ROW EXECUTE FUNCTION kernel.reject_event_change();
CREATE TRIGGER events_no_truncate BEFORE TRUNCATE ON kernel.events
  FOR EACH STATEMENT EXECUTE FUNCTION kernel.reject_event_change();
```
Límite aceptado: un superusuario puede deshabilitar el trigger (`ALTER TABLE ... DISABLE TRIGGER`) o usar `session_replication_role = replica`; eso es DDL deliberado y queda en el registro del servidor, no una escritura accidental. `Down` borra tabla, función y la restricción de `participants`.
`seq` es `GENERATED ALWAYS`: ni la API ni un `INSERT` ordinario del administrador pueden fijarlo.

## Contrato
```go
// model
type NewEvent struct { Type string; Version int; SubjectRef string
    Payload json.RawMessage; Actor Actor; RequestID string; OccurredAt time.Time }
type Event struct { Seq int64; ID, SpaceID string; NewEvent fields...; OccurredAt time.Time }
func (e NewEvent) Validate() error
// state
func AppendEvent(ctx context.Context, tx *sql.Tx, in model.NewEvent) (model.Event, error)
func RecordEvent(ctx context.Context, pool *sql.DB, spaceID string, in model.NewEvent) (model.Event, error)
func ReadEvents(ctx context.Context, pool *sql.DB, spaceID string, fromSeq int64, limit int) ([]model.Event, error)
func ReadEventsTx(ctx context.Context, tx *sql.Tx, fromSeq int64, limit int) ([]model.Event, error)
```
- `AppendEvent` es el helper de HU-kernel-execution-13: nunca abre, confirma ni revierte; ni usa savepoints. Valida primero (`Validate`), así un `invalid_input` no escribe nada ni envenena la transacción del llamador. Genera el UUID v7 en Go y toma el espacio de `kernel.current_space_id()`, nunca del llamador; hace `INSERT ... RETURNING seq`. Sin espacio en la transacción devuelve `rule_violation` (23502 o 42501). `OccurredAt` cero equivale a `now()` de la base. Un error de base deja la transacción abortada: el llamador la revierte.
- `RecordEvent` envuelve `AppendEvent` en `InSpace` (uso autónomo y pruebas).
- `Validate` devuelve `invalid_input` para: `type` fuera del patrón (minúsculas, 2 o más segmentos separados por punto; los ejemplos del modelo como `kernel.space.created` tienen 3), `Version < 1`, `Payload` que no es un objeto JSON válido (vacío, `null`, arreglo o escalar), `Actor.Kind` fuera de los cuatro valores, `ParticipantID` no vacío que no sea UUID no nulo, `RequestID` o `SubjectRef` en blanco si se informan. Sin actor (`ParticipantID` vacío y `Kind != system`) devuelve `permission_denied` (ley de permiso); el `CHECK` es la red de seguridad.
- `ReadEvents` y `ReadEventsTx`: `seq >= fromSeq`, orden `seq ASC`, solo el espacio actual. `fromSeq >= 0` y `1 <= limit <= 1000`, si no `invalid_input`. El consumidor guarda como cursor `ultimo.Seq + 1`.

## Pruebas requeridas
Todas primero (rojas) y con `dbtest.Database` y `dbtest.APIPool`; las de integración fallan, no se omiten.
1. Criterio 1: `TestRecordEventAssignsSeqAndV7ID` en `kernel/state/event_test.go`.
2. Criterio 2: `TestSeqStrictlyIncreasesAcrossTransactions` (dos transacciones consecutivas); `TestRollbackLeavesGapNeverDuplicate` (rollback, el siguiente `seq` salta al menos 2); `TestConcurrentAppendsNeverRepeatSeq` (20 goroutines por 10 inserciones, un tercio con rollback forzado, 4 conexiones: `seq` distintos, crecientes por goroutine, total igual a lo confirmado); `TestSeqIsAllocationOrderNotCommitOrder` (tx1 inserta, tx2 inserta y confirma, un lector ve solo el mayor, tx1 confirma, el lector ve ambos en orden; documenta la garantía). Todas en el mismo archivo.
3. Criterio 3 en `kernel/db/events_schema_test.go`: `TestEventsRejectUpdateDeleteTruncateAsAPI` (rol de la API: `UPDATE`, `DELETE`, `TRUNCATE` dan 42501); `TestEventsRejectUpdateDeleteTruncateAsAdmin` (superusuario: error `restrict_violation` por el trigger, tabla intacta); `TestTriggerHoldsEvenIfGrantedByMistake` (se concede `UPDATE` y `DELETE` a la API dentro de la base de prueba y igual falla por el trigger); `TestEventsSeqCannotBeSet` (`INSERT` con `seq` explícito falla).
4. Criterio 4: `TestNewEventValidateRejectsBadTypeVersionPayload` en `kernel/model/event_test.go` (tabla); `TestRecordEventInvalidInputWritesNothing` en state (cuenta 0 filas); `TestEventsCheckConstraints` en db (los `CHECK` por SQL con administrador).
5. Criterio 5: `TestNewEventValidateRejectsUnknownActorKind` (model) y el caso en `TestEventsCheckConstraints`.
6. Criterio 6: `TestNewEventWithoutActorRejected` (model y state) y `events_actor_required` por SQL (db); además `TestEventActorMustBelongToSpace` (actor de otro espacio falla por la FK compuesta).
7. Criterio 7: `TestEventsIsolationBetweenSpaces` (B lee 0 filas de A) y `TestEventsInsertForeignSpaceRejected` (SQL con rol de la API, espacio B y `space_id = A`, error 42501), en state; `TestEventsForcedRLSAndPolicies` en db (ENABLE, FORCE, solo políticas `SELECT` e `INSERT`, grants exactos).
8. Criterio 8: `TestReadEventsFromSeqAscendingCurrentSpaceOnly` (inclusivo, orden, `limit`, entrelazando eventos de A y B).
9. Helper: `TestAppendEventSharesCallerTransaction` (un `INSERT` ajeno y el evento en la misma transacción; con rollback no queda ninguno) y `TestAppendEventWithoutSpaceFails`.
10. Arquitectura: `kernel/db/adr_test.go` y la prueba de vocabulario deben seguir verdes (ningún término de negocio en SQL, cadenas ni comentarios; usar `kernel.test.recorded`).

## Riesgos técnicos y mitigación
- **Orden de lectura con transacciones concurrentes.** `seq` se asigna al insertar, no al confirmar: una transacción con `seq` menor puede confirmarse después de otra con `seq` mayor, y un lector con cursor `seq >= n` saltaría ese evento para siempre. Decisión: el kernel garantiza (a) `seq` único y creciente por asignación en todo el sistema, (b) huecos permitidos, (c) cada lectura devuelve solo eventos confirmados en orden `seq` ascendente. No garantiza que lecturas sucesivas no revelen después un `seq` menor al ya visto. Se descartó serializar escritores con un bloqueo asesor (cuello de botella global y sin garantía estricta de orden de confirmación). El outbox de HU-kernel-outbox-10 no sufre el problema porque se reclama por estado y `next_attempt_at`, no por cursor de `seq`. Todo consumidor que lea por cursor (proyecciones, B3) debe reanudar con un traslape de ventana y deduplicar por `id`, o exigir un horizonte estable; la opción robusta (columna `xid8 DEFAULT pg_current_xact_id()` y filtro con `pg_snapshot_xmin`) se decide en B3 con su ADR. Queda registrado en ADR-0028.
- **Rechazo en base de datos para el dueño.** El trigger protege contra errores, no contra un superusuario deliberado; B6 puede añadir auditoría de DDL.
- **FK compuesta del actor.** Exige `UNIQUE (id, space_id)` en `participants` (toca la tabla de 00005; no cambia datos). Si un participante archivado actúa, sigue válido (historia).
- **`payload` sin tamaño máximo.** Fuera de la historia; dejar el límite al contrato de B3. Anotar en seguimientos.
- **Contención en la secuencia global.** `nextval` no bloquea; el índice `(space_id, seq)` crece con la tabla. Particionado queda para B6.
- **`UPDATE` sobre `events` con RLS.** Sin política de `UPDATE` ni `DELETE` y sin grant, el rol de la API recibe 42501, no 0 filas; las pruebas esperan 42501.

## Contradicciones historia vs documentación (con recomendación)
1. Criterio 4 dice `<namespace>.<name>`, pero el modelo usa tipos de tres segmentos (`kernel.space.created`, y el propio `kernel.test.recorded`). Recomendación: patrón de 2 o más segmentos, como en el SQL de arriba.
2. El ejemplo de `kernel.md` muestra `business.payment.received.v1` mientras `version` ya es columna. Recomendación: sin sufijo de versión en `type`; corregir el ejemplo en la documentación.
3. "Desde `seq = n`": se interpreta inclusivo (`>=`). Recomendación: confirmarlo en el PR; el cursor del consumidor es último más uno.
4. Criterio 6 dice solo "se rechaza", sin código. Recomendación: `permission_denied` en Go, `CHECK` como respaldo.
5. `kernel.md` pide `payload` "validado con JSON Schema"; M1 solo exige objeto JSON (deuda controlada hasta B3, ya aceptada).
6. La regla de memoria dice "nunca borrando": ni siquiera el dueño. Los grants solos no lo logran; de ahí el trigger, que la historia pide pero la documentación no menciona. Recomendación: documentarlo en `kernel.md`.

## Fuera de alcance
Outbox y filas por suscriptor (HU-10), permisos y motor de ejecución (HU-13), reloj inyectable, JSON Schema y `contracts/events/` (B3), proyecciones, suscripciones, `webhook_*`, `notices`, límite de tamaño del `payload`, columna de horizonte de transacción, particionado y retención.
