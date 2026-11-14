# Diseño: HU-kernel-idempotency-14

## Resumen
Migración `00014_idempotency.sql`: `kernel.idempotency_records` de solo agregar (una fila por ejecución, sin UPDATE, DELETE ni TRUNCATE), y `Engine.RunIdempotent`, que bajo un bloqueo asesor por (espacio, clave) devuelve la respuesta guardada, rechaza con `idempotency_conflict` otra huella o ejecuta el manejador y guarda el registro en la misma transacción. Estado: lista, sin bloqueo; el ADR-0026 y las enmiendas de ADR-0004, ADR-0030 y `contrato.md` van ANTES del código. La nota excede 150 líneas por el SQL y los ataques, como timers-12.

## Tipo de trabajo
`backend`: SQL y Go del kernel; sin contrato público ni app.

## Módulos a tocar
- `kernel/model/errors.go`: `CodeIdempotencyConflict = "idempotency_conflict"`; `errors_test.go` lo cubre.
- `kernel/execution/engine.go`: `Call` suma `IdempotencyKey` y `Request`; `RunIdempotent`; `Run` rechaza (`invalid_input`) una operación `Idempotent` (esa va por `RunIdempotent`). Comentarios de `Run` y de `Handler`.
- `kernel/execution/operation.go`: el comentario de `Idempotent` ya no dice "lo implementa 14".
- `kernel/execution/tx.go`: `Tx` suma `executionID` y un contador; `RecordExchange` estampa la clave del intercambio (ver Contrato). Ayuda también al seguimiento 36 (comentario en `AppendExchange` sobre `SET CONSTRAINTS ... DEFERRED`, una línea en `kernel/state/exchange.go`).
- `kernel/execution/errors.go`: `classify` mapea `idempotency_records_pkey` y `idempotency_records_key_in_force` (23505) a `rule_violation` tipado; nunca se filtra un 23505 crudo.
- Pruebas existentes que dejan de valer: las que declaran `Idempotent: true` y llaman a `Run` (`kernel/execution/world_test.go`, `operation_test.go`, `kernel/laws/execution_atomicity_test.go`, `kernel/state/account_test.go`): pasar a `RunIdempotent` con clave y cuerpo. `grep -rn "timers" kernel/db/*_test.go` para sumar `idempotency_records` a toda lista de tablas (RLS, TRUNCATE, aislamiento, `migrate_test.go`).
- Docs: `docs/02-arquitectura/contrato.md` (código en la lista de errores y una frase sobre clave y huella), `docs/05-decisiones/0004-contrato-unico.md` (sección "Enmienda 2026-11-14": añade `idempotency_conflict`, remite al 0026), `docs/05-decisiones/0030-libro-de-doble-partida.md` (la frase "por ahora la clave repetida se rechaza" pasa a: el motor deriva la clave del intercambio de la ejecución), `docs/05-decisiones/0035-motor-de-ejecucion.md` (idempotencia hecha), `docs/05-decisiones/README.md` (índice), `docs/02-arquitectura/kernel.md` (motor), `docs/02-arquitectura/modelo-de-datos/kernel.md` (sección 5: columnas, ER y regla de inmutabilidad), `kernel/README.md`, estado del backlog y `seguimientos.md`.

## Archivos nuevos
- `docs/05-decisiones/0026-idempotencia-por-registro.md` (formato de 0034; lo valida `adr_test.go`). Contenido: (1) por qué un registro por (espacio, clave) y no solo `exchanges.idempotency_key`; (2) huella SHA-256 sobre serialización canónica, sin guardar el cuerpo; (3) bloqueo asesor por (espacio, clave) en lugar de `ON CONFLICT` o `FOR UPDATE` (el registro no existe antes de que corra el manejador y no puede actualizarse después); (4) tabla de solo agregar con generaciones, y la purga diferida; (5) `idempotency_conflict` (no `already_done`: "ya se hizo"; no `invalid_input`: no es un error de formato); (6) la clave del intercambio la deriva el motor; (7) el reloj inyectable solo puede ir por detrás del de la base; (8) límites: el rol de la API es el proceso del kernel (seguimiento 42) y un administrador puede deshabilitar disparadores (ADR-0028). Cumplimiento: pruebas de abajo y `adr_test.go`.
- `kernel/db/migrations/00014_idempotency.sql`.
- `kernel/model/idempotency.go`: constantes, `ValidateIdempotencyKey`, `CanonicalJSON`, `RequestHash`, `IdempotencyRecord`.
- `kernel/state/idempotency.go`: `LockIdempotencyKey`, `FindIdempotencyRecord`, `AppendIdempotencyRecord`. Vive en `state` porque solo `state` toca la base.
- Pruebas: `kernel/model/idempotency_test.go`, `kernel/db/idempotency_schema_test.go`, `kernel/db/idempotency_security_test.go`, `kernel/state/idempotency_test.go`, `kernel/execution/idempotency_test.go`, `kernel/execution/idempotency_concurrency_test.go`.

## Cambios de datos
(a) Una fila por ejecución: reemplazar un registro vencido sería un UPDATE o un DELETE, y la inmutabilidad en dos capas no admite ninguno. La PK incluye `generation`: la nueva ejecución tras el vencimiento inserta la generación siguiente y la vigente es la mayor. Así la purga queda fuera sin agujero: no hay ruta de borrado; una migración futura (retención, junto con los seguimientos 63 y 68) añadirá un borrado acotado a vencidos con su ADR.
```sql
CREATE TABLE kernel.idempotency_records (
  id uuid NOT NULL CHECK (substr(id::text, 15, 1) = '7'),         -- v7 from Go; also the execution id
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  key text NOT NULL CONSTRAINT idempotency_records_key_check CHECK (key ~ '^[A-Za-z0-9._:-]{1,128}$'),
  generation integer NOT NULL CHECK (generation >= 1),             -- stamped by trigger
  operation text NOT NULL CHECK (char_length(operation) <= 128 AND operation ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'),
  actor_participant_id uuid NOT NULL,
  request_hash bytea NOT NULL CONSTRAINT idempotency_records_hash_size CHECK (octet_length(request_hash) = 32),
  response jsonb NOT NULL CONSTRAINT idempotency_records_response_check
    CHECK (jsonb_typeof(response) = 'object' AND octet_length(response::text) <= 65536),
  created_at timestamptz NOT NULL,                                 -- Go clock, capped by trigger at now()
  expires_at timestamptz NOT NULL,                                 -- stamped by trigger
  PRIMARY KEY (space_id, key, generation),
  CONSTRAINT idempotency_records_id_space_key UNIQUE (id, space_id),
  CONSTRAINT idempotency_records_ttl CHECK (expires_at = created_at + interval '24 hours'),
  CONSTRAINT idempotency_records_actor_fkey FOREIGN KEY (actor_participant_id, space_id)
    REFERENCES kernel.participants (id, space_id));
-- ENABLE y FORCE RLS; politicas solo SELECT e INSERT con (SELECT kernel.current_space_id()); sin politica de UPDATE ni DELETE.
GRANT SELECT ON kernel.idempotency_records TO nexusthrive_api;
GRANT INSERT (id, space_id, key, operation, actor_participant_id, request_hash, response, created_at)
  ON kernel.idempotency_records TO nexusthrive_api;   -- sin generation ni expires_at; sin UPDATE, DELETE, TRUNCATE
GRANT EXECUTE ON FUNCTION kernel.idempotency_lock(text) TO nexusthrive_api;
```
Funciones: `plpgsql`, `SET search_path = pg_catalog, pg_temp`, sin `SECURITY DEFINER`, valen para el dueño.
1. `kernel.idempotency_lock(key text) RETURNS void`: `pg_advisory_xact_lock(hashtextextended(kernel.current_space_id()::text || '/' || key, 0))`; sin espacio actual, `rule_violation`. La usan Go y el disparador (una sola definición). Un choque de hash solo espera de más, nunca decide.
2. `idempotency_records_guard_insert` BEFORE INSERT: `space_id` = espacio actual (si no, `restrict_violation`); `created_at > now()` rechaza (techo: el reloj inyectable puede ir por detrás, nunca adelante); toma `idempotency_lock`; lee la mayor generación de (`space_id`, `key`): si existe y `expires_at > now()`, error `unique_violation` con `CONSTRAINT = 'idempotency_records_key_in_force'`; `NEW.generation := prior + 1` (o 1); `NEW.expires_at := NEW.created_at + interval '24 hours'`.
3. `kernel.reject_idempotency_change()`: `restrict_violation` en `BEFORE UPDATE` y `BEFORE DELETE` por fila y en `BEFORE TRUNCATE` por sentencia (cubre `TRUNCATE ... CASCADE` desde `spaces` y `participants`).
Desviación consciente de la lección "la API no fija created_at": criterio 1 y 6 exigen reloj inyectable, y la base no lo ve (precedente de `next_attempt_at` en outbox y del techo `LEAST` en timers). Se acota con el techo y el CHECK; `expires_at` y `generation` nunca los escribe un llamador. Quien tenga el rol de la API puede, como mucho, insertar un registro ya vencido (inocuo: la siguiente ejecución lo reemplaza).

## Contrato
```go
// model
const IdempotencyTTL = 24 * time.Hour // = the SQL interval; one test compares both
const MaxIdempotencyKey, MaxRequestBody, MaxResponseBody = 128, 1 << 20, 64 << 10
func ValidateIdempotencyKey(key string) error // pattern ^[A-Za-z0-9._:-]{1,128}$, else invalid_input
func CanonicalJSON(body []byte) ([]byte, error)
func RequestHash(operation string, actor Actor, body []byte) ([32]byte, error)
// execution
type Call struct { Actor; SpaceID, RequestID string; IdempotencyKey string; Request json.RawMessage }
type ResultHandler func(ctx context.Context, tx *Tx) (json.RawMessage, error)
type Response struct { Body json.RawMessage; Replayed bool }
func (e *Engine) RunIdempotent(ctx context.Context, call Call, name string, fn ResultHandler) (Response, error)
// state (in the caller's transaction, space already set)
func LockIdempotencyKey(ctx context.Context, tx *sql.Tx, key string) error
func FindIdempotencyRecord(ctx context.Context, tx *sql.Tx, clock model.Clock, key string) (model.IdempotencyRecord, bool, error)
func AppendIdempotencyRecord(ctx context.Context, tx *sql.Tx, clock model.Clock, in model.NewIdempotencyRecord) (model.IdempotencyRecord, error)
```
(b) Huella: `CanonicalJSON` recorre `json.Decoder` con `UseNumber` y emite: objetos con claves ordenadas bytewise, sin espacios; números como su texto literal (nunca `float64`; `1` y `1.0` son cuerpos distintos, la salida es conservadora); cadenas con `json.Marshal`. Rechaza con `invalid_input`: UTF-8 inválido, claves duplicadas (el decodificador estándar se queda con la última y haría ambiguo el cuerpo), profundidad mayor a 64, más de 1 MiB, bytes sobrantes tras el valor y cuerpo vacío. `RequestHash` = SHA-256 de campos con prefijo de largo de 8 bytes: etiqueta `nexusthrive.idempotency.v1`, operación, tipo y participante del actor, cuerpo canónico. Incluir operación y actor evita que otra operación u otro actor con la misma clave reciba la respuesta ajena (sale `idempotency_conflict`, sin revelar nada). No se guarda el cuerpo: puede tener datos sensibles, ocupa y no hace falta para comparar.
(d) Flujo de `RunIdempotent`, en este orden, cada paso detiene la llamada si falla: `prepare` (registrada, `Idempotent` obligatoria, id de solicitud, espacio); clave ausente o inválida `invalid_input` (criterio 9) y cuerpo no canónico `invalid_input`, ambos antes de abrir nada; `InSpace`; permisos con `Authorizer.Check` (antes de tocar registros: quien no tiene permiso no sondea claves); `LockIdempotencyKey`; `FindIdempotencyRecord` (la consulta compara `expires_at > LEAST($clock, now())`, así Go y la base coinciden); si hay vigente: misma huella devuelve `Response{Replayed: true}` sin manejador (`bytes.Equal`), otra huella `idempotency_conflict` con mensaje fijo y sin escribir; si no hay o venció: `executionID = UUIDv7`, manejador, validar respuesta (JSON objeto, hasta 64 KiB, si no `invalid_input`), `AppendIdempotencyRecord` (`RETURNING response::text`), y el cuerpo que se devuelve es el guardado, byte a byte el de una repetición. Todo en una transacción: un fallo del manejador o de un `COMMIT` diferido no deja registro (criterio 7). El bloqueo vive hasta el commit: la segunda solicitud espera al ganador, relee y devuelve la misma respuesta, o ejecuta si el ganador falló. El `ctx` acota la espera.
(c) Por qué bloqueo asesor: `ON CONFLICT DO NOTHING` exigiría insertar antes del manejador, con la respuesta aún desconocida y sin poder actualizarla; `FOR UPDATE` no bloquea una fila que no existe. Ambos concurrentes verían "no hay registro" y ejecutarían dos veces.
Clave del intercambio: `Tx.RecordExchange` en una ejecución idempotente rechaza una `IdempotencyKey` del manejador (`invalid_input`) y estampa `<executionID>:<n>`. Con la clave del cliente como clave del intercambio, el criterio 6 sería imposible: `exchanges_idempotency_key` es única para siempre. Orden: `idempotency_conflict` y la repetición cortan antes del manejador, así que la segunda barrera solo atrapa a una misma ejecución que repita (defensa en profundidad). `Run` (sin clave) conserva la clave del manejador.

## Pruebas requeridas
Primero y en rojo, con PostgreSQL real (`dbtest`), sin `t.Skip`. Orden: `model`, `db`, `state`, `execution`. Los fixtures de tiempo usan relojes por detrás de la base (`now() - 25h`), nunca adelante.
- C1: `TestFirstRunStoresHashResponseAndExpiryInSameTransaction` (execution: fila con huella, respuesta, `expires_at = created_at + 24h` con el reloj; `TestFailedCommitLeavesNoRecordNorExchange` con fallo inyectado tras el insert).
- C2: `TestRepeatWithSameKeyAndBodyReplaysWithoutHandler` (manejador contado una vez; filas de `exchanges` y `entries` iguales; cuerpo idéntico byte a byte; `Replayed`).
- C3: `TestRepeatWithOtherBodyIsIdempotencyConflictAndWritesNothing` (conteos iguales; tipo y mensaje fijo), `TestOtherOperationOrActorWithSameKeyIsConflict`.
- C4: `TestSameKeyInOtherSpaceIsFirstRun`.
- C5: `TestConcurrentSameKeyAndBodyRunsOnce` (2 a 8 goroutines con barrera, manejador que duerme 50 ms, 20 repeticiones: manejador 1 vez, un intercambio, un registro, respuestas idénticas, un solo `Replayed == false`, ningún error), `TestConcurrentSameKeyOtherBodyOneWinnerOtherConflict`, `TestConcurrentFailureIsNotCachedAndSecondRuns`, `TestEngineLockOrderNoDeadlockWithRevocation` (50 vueltas, sin 40P01).
- C6: `TestExpiredRecordIsReplacedByNextGeneration` (reloj en `T`, ejecutar, reloj a `T + 24h + 1s` dentro del techo de la base: dos intercambios con claves distintas, generación 2, la 1 intacta), `TestRecordAt23h59IsStillInForce`.
- C7: `TestRuleErrorIsNotCachedAndRetryRuns`, `TestHandlerPanicLeavesNoRecord`.
- C8: `TestIdempotencyRecordsIsolationBetweenSpaces` (B lee 0 filas; sin espacio actual 0 filas).
- C9: `TestMoneyOperationWithoutKeyIsInvalidInput` (clave vacía, con espacios, con salto de línea final, de 129 caracteres: sin transacción abierta ni escritura), `TestRunRejectsIdempotentOperation`.
- Huella: `TestCanonicalJSONIgnoresKeyOrderAndSpaces`, `TestCanonicalJSONRejectsDuplicateKeysBadUTF8DepthAndSize`, `TestRequestHashDiffersByOperationActorAndBody`, vector fijo de SHA-256 (fija el formato v1).
- Ataques (rol de la API y administrador): `TestAPICannotUpdateDeleteOrTruncateRecords` (42501 al API; `restrict_violation` al administrador, también `TRUNCATE ... CASCADE` desde `spaces` y `participants`), `TestAPICannotWriteManagedColumns` (INSERT con `generation` o `expires_at`: 42501), `TestInsertCannotBeBornInTheFuture` (`created_at > now()`), `TestInsertInKeyInForceRejected` (23505 `key_in_force`, saltando el motor), `TestInsertOfOtherSpaceRejected` (RLS y FK compuesta, también un `actor_participant_id` de otro espacio), `TestOnConflictCopyAndSetRoleDoNotBypass` (`ON CONFLICT DO UPDATE` sin privilegio de UPDATE; `COPY`; `SET ROLE`), `TestSessionReplicationRoleAndAlterTableDeniedToAPI` (42501), `TestRecordWithoutExchangeIsHarmlessButOwnSpaceOnly` (un registro falsificado en el propio espacio solo provoca conflicto en esa clave y no cruza espacios; documenta el límite), `TestHashCollisionCheckIsFullLength` (huellas de 31 y 33 bytes rechazadas por el CHECK; un cambio de un bit en la huella guardada da conflicto), `TestSpaceSwitchBeforeCommitFailsClosed`, `TestIdempotencyForcedRLSAndGrants` (ENABLE, FORCE, solo SELECT e INSERT, `column_privileges` exactos), `TestIdempotencyFunctionsPinSearchPathAndNotSecurityDefiner`, `TestTTLConstantMatchesSQL`.
- Resto: `errors_test.go` (código nuevo en su lista), `architecture_test.go` y `adr_test.go` en verde, sin vocabulario de negocio en SQL ni cadenas.
- Mutaciones de una línea antes del PR (cada una debe romper una prueba): quitar `LockIdempotencyKey` del motor, del disparador o ambos; `>` por `>=` en `expires_at > LEAST(...)`; quitar `LEAST(..., now())`; quitar el techo `created_at > now()`; devolver el cuerpo del manejador en vez del guardado; quitar la operación o el actor de `RequestHash`; quitar la comparación de huellas; mover `Check` después del bloqueo; añadir `generation` o `expires_at` al GRANT; añadir UPDATE o DELETE al GRANT; quitar un disparador de rechazo; quitar el CHECK de TTL; quitar `key_in_force` del disparador; sacar el insert de la transacción; guardar el registro aunque falle el manejador.

## Riesgos técnicos y mitigación
- **Contradicciones y recomendaciones.** 1) Criterio 6 contra `exchanges.idempotency_key` única para siempre (ADR-0030, exchanges-08): con la clave del cliente en el libro, una operación nueva tras 24 horas fallaría. Recomendado: el motor deriva la clave del intercambio de la ejecución (arriba) y se enmienda ADR-0030. Alternativa descartada: relajar la unicidad del libro, que rompe la segunda barrera y la ley de memoria. 2) La historia pide `created_at` con reloj inyectable y la lección pide que la API no lo fije: se acota con techo y CHECK (arriba); alternativa descartada: `now()` de la base, que impide probar el vencimiento sin dormir 24 horas. 3) "Reemplazar el registro vencido" contra la inmutabilidad: se resuelve con generaciones, no con UPDATE. 4) El motor no tiene respuesta tipada: se define `json.RawMessage` objeto y `Response`; `Run` se conserva para operaciones sin idempotencia, de modo que los manejadores actuales no cambian de firma. 5) Criterio 1 dice "huella de C": el motor no ve el cuerpo si el gateway no se lo da; `Call.Request` es obligatorio y B3 lo llenará con el cuerpo recibido.
- **El rol de la API es el proceso del kernel** (seguimientos 42, 45): quien lo controle puede insertar registros falsos en su espacio (provoca conflicto o repite una respuesta inventada) y deshabilitar disparadores si es dueño (ADR-0028). Quedan CHECK, PK, FK compuesta, RLS y grants. Un manejador no tiene SQL (el `Tx` no lo expone).
- **Espera larga:** la segunda solicitud espera todo el manejador del ganador; el `ctx` de B3 la acota. Un administrador con `pg_advisory_lock` propio podría detener una clave; es DoS del operador, no del cliente.
- **Crecimiento sin purga:** una fila por ejecución, y por clave tras cada vencimiento. Seguimiento nuevo: retención de `idempotency_records` (junto con 63), B6. La purga futura será un borrado de solo vencidos, con ADR.
- **Reloj:** reloj inyectable adelantado sobre la base: `LEAST` lo acota en lectura y el techo en escritura; atrasado: el registro vive más tiempo (seguro: no se duplica dinero). `now()` es el inicio de la transacción; una transacción larga ve un reloj atrasado, también seguro.
- **Respuesta en `jsonb`:** normaliza espacios y orden de claves; la primera ejecución devuelve ya la forma guardada. Cantidades van como cadenas decimales en la respuesta (regla de dinero exacto); el manejador es responsable de no usar `float`.
- **Seguimientos.** 36: se cierra la mitad del comentario sobre `SET CONSTRAINTS` (queda la prueba Up-Down-Up de 00009, igual que 38 con el ejecutor de Down). 50: ya Hecho en execution-13, sin tocar. 58: sin relación (cancelar con `canceled_reason` nulo), sigue diferido a `laws-15`. Nuevos, desde el siguiente número libre: retención de registros; `Call.Request` lo llena B3; `RequestHash` v1 y cómo versionarlo.
- Inconsistencia de docs: la tabla de Definition of Ready del backlog sigue marcando idempotency-14 con "Sin TBD = pendiente" (plan.md, línea 105); la corrige el Requirements Analyst.

## Fuera de alcance
Purga automática o manual de vencidos (la migración no concede ni DELETE ni su excepción), idempotencia a nivel de puerta o de cabecera HTTP (B3), proceso programador, normalización Unicode o numérica del cuerpo, idempotencia de operaciones `read` o `write` no declaradas, reversiones (`AppendReversal` no se expone por `Tx`), respuestas por encima de 64 KiB.
