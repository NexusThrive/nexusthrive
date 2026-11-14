# Diseño: HU-kernel-participants-04

## Resumen
Se crean `kernel.identities` (global, sin `space_id`, visible solo a través de un participante del espacio actual) y `kernel.participants` (con `space_id` y RLS), más `state.CreateIdentity`, `state.CreateParticipant` y `state.ArchiveParticipant`. Antes, una migración pequeña de endurecimiento (observaciones N1 y N2 de seguridad) y una enmienda fechada al ADR-0027, para que las tablas siguientes copien el patrón ya corregido.

## Tipo de trabajo
`backend`: SQL, Go y pruebas de integración; no hay pantallas ni cliente.

## Decisiones (justificadas)
1. **Quién escribe en `identities`: política `INSERT` acotada, no `SECURITY DEFINER`.** Con `FORCE`, la política también rige al dueño no superusuario, así que una función `SECURITY DEFINER` necesitaría igualmente una política de inserción o que el dueño tenga `BYPASSRLS` (superusuario en local y CI, `BYPASSRLS` en Supabase): comportamiento distinto por entorno. Además agrega una superficie de escalada (dueño privilegiado, `search_path`, `EXECUTE`). La política `FOR INSERT WITH CHECK (true)` más `GRANT SELECT, INSERT` (sin `UPDATE`, `DELETE`, `TRUNCATE`) no deja leer, modificar ni borrar lo insertado. Costo aceptado: cualquier conexión de la API puede crear identidades (spam), mitigado aguas arriba en B3 (limitación de tasa al validar el token).
2. **Sin `RETURNING` en `identities`.** `INSERT ... RETURNING` exige que la fila nueva pase la política `SELECT`, y una identidad sin participante no la pasa. El id UUID v7 se genera en Go (A4) y no se usa `ON CONFLICT` (también exige `SELECT`).
3. **`CreateIdentity` corre sin espacio.** Una identidad nace antes que cualquier participante (primer inicio de sesión). Se factoriza en `transaction.go` un `inTransaction` no exportado que `InSpace` reutiliza (mismo comportamiento, lo cubren sus pruebas); `CreateIdentity` lo usa sin fijar espacio. No se exporta: todo acceso a datos de espacio sigue pasando por `InSpace`.
4. **`INSERT` y claves foráneas.** La comprobación de una clave foránea ignora la RLS (documentación de PostgreSQL), por lo que `participants.identity_id` puede apuntar a una identidad que el espacio aún no ve; al insertar el participante la identidad pasa a ser visible.
5. **`UPDATE` mínimo en `participants`:** `GRANT UPDATE (status, archived_at)` por columna. `space_id`, `identity_id` y `kind` son inmutables para la API. `spaces` concedió `UPDATE` completo; aquí se endurece porque la historia solo archiva.
6. **Numeración:** `00004` endurecimiento, `00005` identidades y participantes (una sola migración, atómica; la política de `identities` necesita `participants`).
7. **Códigos de error:** `kernel/model` añade `CodeNotFound` (ya listado en `contrato.md`). Archivar un participante ya archivado es idempotente (retorna nil sin cambiar `archived_at`); no se añade `already_done`.
8. **Actor:** igual que `CreateSpace`, un `Actor.Kind` distinto de `system` (incluido el vacío) devuelve `permission_denied` hasta que lleguen los permisos (HU-06/13). Adición del diseño, no es criterio.

## Módulos a tocar
- `kernel/state/transaction.go`: extrae `inTransaction`; `InSpace` lo usa.
- `kernel/model/errors.go`: añade `CodeNotFound`.
- `docs/05-decisiones/0027-roles-y-rls.md`: sección "Enmienda 2026-11-13" (ver abajo). No cambia la decisión ni el estado.
- `docs/02-arquitectura/modelo-de-datos/kernel.md`: Aislamiento (políticas por comando en tablas sin `space_id`; `search_path` fijo) y nota del índice único `(space_id, identity_id)`.

## Archivos nuevos
- `kernel/db/migrations/00004_hardening.sql`: guarda ampliada del rol y `search_path` fijo en `current_space_id()`.
- `kernel/db/migrations/00005_participants.sql`: tablas, índice único, RLS, políticas y grants.
- `kernel/model/participant.go`: `IdentityProvider`, `ParticipantKind`, `ParticipantStatus`, `DocumentType` con `Valid()`.
- `kernel/state/identity.go`: `CreateIdentity`. `kernel/state/participant.go`: `CreateParticipant`, `ArchiveParticipant`, traducción de errores.
- Pruebas: ver más abajo.

## Cambios de datos
```sql
-- 00004_hardening.sql (no se edita la 00001: ya está aplicada en entornos)
-- +goose Up
-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'nexusthrive_api' AND (rolsuper OR rolbypassrls
      OR rolcreaterole OR rolcreatedb OR rolreplication)) THEN
    RAISE EXCEPTION 'role nexusthrive_api has attributes that weaken isolation';
  END IF; END $$;
-- +goose StatementEnd
CREATE OR REPLACE FUNCTION kernel.current_space_id() RETURNS uuid
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp
AS $$ SELECT nullif(current_setting('kernel.space_id', true), '')::uuid $$;
```
`CREATE OR REPLACE` conserva el `GRANT EXECUTE`. Con `SET` la función ya no se expande en línea; el costo es nulo porque las políticas la llaman como `(SELECT kernel.current_space_id())` (una vez por consulta).

**Por qué migración y no función de verificación (N1):** una migración corre en todo despliegue, en bases nuevas y en las que ya aplicaron la 00001; una función solo protege si alguien la llama. Si un entorno tiene el rol con `CREATEROLE`, la migración falla y el despliegue se detiene (cierra por defecto); el operador lo corrige con `ALTER ROLE nexusthrive_api NOCREATEROLE NOCREATEDB NOREPLICATION`.

```sql
-- 00005_participants.sql (resumen; el desarrollador escribe el archivo completo)
CREATE TABLE kernel.identities (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  provider text NOT NULL CHECK (provider IN ('clerk','api_key','system')),
  subject text NOT NULL CHECK (btrim(subject) <> ''),
  primary_email text CHECK (primary_email IS NULL OR btrim(primary_email) <> ''),
  display_name text NOT NULL CHECK (btrim(display_name) <> ''),
  last_seen_at timestamptz, disabled_at timestamptz,
  CONSTRAINT identities_provider_subject_key UNIQUE (provider, subject));
CREATE TABLE kernel.participants (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  identity_id uuid REFERENCES kernel.identities (id),
  kind text NOT NULL CHECK (kind IN ('person','organization','agent','system')),
  display_name text NOT NULL CHECK (btrim(display_name) <> ''),
  legal_name text,
  document_type text CHECK (document_type IN ('CC','NIT','CE','PP')),
  document_number text,
  country_code text CHECK (country_code ~ '^[A-Z]{2}$'),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived')),
  archived_at timestamptz,
  CHECK ((status = 'archived') = (archived_at IS NOT NULL)),
  CHECK ((document_type IS NULL) = (document_number IS NULL)));
CREATE UNIQUE INDEX participants_space_identity_key
  ON kernel.participants (space_id, identity_id) WHERE identity_id IS NOT NULL;
-- ENABLE y FORCE en ambas tablas
CREATE POLICY participants_isolation ON kernel.participants
  USING (space_id = (SELECT kernel.current_space_id()))
  WITH CHECK (space_id = (SELECT kernel.current_space_id()));
CREATE POLICY identities_visible_via_participant ON kernel.identities FOR SELECT
  USING (EXISTS (SELECT 1 FROM kernel.participants p WHERE p.identity_id = identities.id
                 AND p.space_id = (SELECT kernel.current_space_id())));
CREATE POLICY identities_insert ON kernel.identities FOR INSERT WITH CHECK (true);
GRANT SELECT, INSERT ON kernel.identities TO nexusthrive_api;
GRANT SELECT, INSERT ON kernel.participants TO nexusthrive_api;
GRANT UPDATE (status, archived_at) ON kernel.participants TO nexusthrive_api;
```
El índice único sirve también a la política de `identities`. La política de visibilidad no filtra por `status`: un participante archivado sigue haciendo visible su identidad (criterio 7).

## Contrato
```go
// kernel/model: tipos string con Valid(); CodeNotFound Code = "not_found"
// kernel/state
type NewIdentity struct{ Provider model.IdentityProvider; Subject, PrimaryEmail, DisplayName string }
func CreateIdentity(ctx context.Context, pool *sql.DB, actor model.Actor, in NewIdentity) (string, error)
type NewParticipant struct {
    Kind model.ParticipantKind; IdentityID, DisplayName, LegalName string
    DocumentType model.DocumentType; DocumentNumber, CountryCode string // "" = null
}
func CreateParticipant(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID string, in NewParticipant) (string, error)
func ArchiveParticipant(ctx context.Context, pool *sql.DB, actor model.Actor, spaceID, participantID string) error
```
- Validaciones en Go antes de la base (`invalid_input`): proveedor y tipo conocidos, `subject` y `display_name` no vacíos, `identity_id` vacío o UUID no nulo, tipo de documento válido y par tipo/número, `country_code` de dos mayúsculas. Los `CHECK` son la segunda barrera.
- Traducción SQLSTATE: `23505` a `rule_violation` (identidad o `(space_id, identity_id)` repetidos); `23514` y `22P02` a `invalid_input`; `23503` por restricción: `identity_id` a `invalid_input`, `space_id` a `not_found`.
- `CreateParticipant` genera UUID v7 y corre en `InSpace(spaceID)` con `space_id` tomado de ese espacio (nunca del llamador); `status` queda por defecto `active`.
- `ArchiveParticipant`: `UPDATE ... SET status='archived', archived_at=now() WHERE id=$1 AND status='active'`; si afecta 0 filas consulta el id: no existe o es de otro espacio, `not_found`; ya archivado, nil.

## Pruebas requeridas (escribirlas primero y verlas fallar)
`dbtest.Database` (administrador) y `dbtest.APIPool` (rol limitado), igual que en spaces-03.
- `kernel/db/hardening_test.go`: `TestCurrentSpaceIDPinsSearchPath` (`proconfig` fija `search_path`); `TestCurrentSpaceIDIgnoresShadowingFunction` (el administrador crea `public.current_setting(text, boolean)` devolviendo otro UUID; con `SET LOCAL search_path = public, pg_catalog` la función sigue devolviendo el espacio fijado; falla antes de la 00004); `TestCurrentSpaceIDMalformedValueFails` (N2: `'abc'` da `22P02`; `''` da NULL; UUID válido lo devuelve); `TestRoleGuardRejectsDangerousAttributes` (N1: en una transacción del administrador, `ALTER ROLE nexusthrive_api <atributo>` para cada uno de los cinco, ejecuta el primer bloque `StatementBegin/End` del archivo leído del `embed`, espera error y hace `ROLLBACK`; `ALTER ROLE` es transaccional e invisible a otras sesiones).
- `kernel/db/participants_schema_test.go`: `TestIdentitiesTableShape` (sin `space_id`, único `(provider, subject)`, `CHECK` de proveedor, RLS `ENABLE` y `FORCE`, `pg_policies` solo con `SELECT` e `INSERT`); `TestParticipantsTableShape` (columnas, `space_id NOT NULL`, índice único parcial, RLS forzada); `TestAPIRoleGrantsOnParticipantTables` (sin `UPDATE` en `identities`, sin `DELETE`/`TRUNCATE` en ambas, `UPDATE` solo en `status` y `archived_at` con `has_column_privilege`).
- `kernel/state/identity_test.go`: criterio 1 `TestCreateIdentityDuplicateIsRuleViolation` (una sola fila vista por el administrador); criterio 2 `TestCreateIdentityRejectsUnknownProvider` (`""`, `google`; 0 filas); `TestCreateIdentityReturnsV7`; `TestCreateIdentityStaysInvisibleWithoutParticipant` (el administrador ve 1; con `InSpace(A)` el rol de la API ve 0); `TestIdentityOperationsRejectNonSystemActor`.
- `kernel/state/participant_test.go`: criterio 3 `TestIdentityVisibleOnlyThroughParticipantInCurrentSpace` (identidad X con participantes en A y B, identidad Y solo en B: en A se ve el participante de A y X, no Y; en B se ven el de B, X e Y); criterio 4 `TestCreateParticipantRejectsUnknownKind` (0 filas); criterio 5 `TestCreateParticipantWithoutIdentity` (`person`, `identity_id` nulo, `status = active`); `TestCreateParticipantSecondForSameIdentityIsRuleViolation` (mismo espacio falla; otro espacio y dos sin identidad se aceptan); `TestCreateParticipantRejectsBadDocumentAndCountry`; `TestCreateParticipantUnknownIdentity`; criterio 6 `TestParticipantIsolatedAcrossSpaces` (en B: `SELECT` 0 filas, `UPDATE` afecta 0 filas, `INSERT` con `space_id = A` falla con `42501`, `UPDATE ... SET space_id` falla con `42501` por el grant de columnas); criterio 7 `TestArchiveParticipantKeepsItReadable` (`archived`, `archived_at` fijado, `SELECT` lo ve, segundo archivado no cambia `archived_at`); `TestArchiveParticipantUnknownOrOtherSpaceIsNotFound`; `TestParticipantOperationsRejectNonSystemActor`.
- `kernel/model/participant_test.go`: `Valid()` de cada tipo. `kernel/architecture_test.go` debe seguir en verde (escanea las migraciones nuevas).

## Orden de trabajo
1. Enmienda al ADR-0027 (commit propio; sección fechada que dice: `search_path` fijo en `current_space_id()`; la guarda del rol comprueba cinco atributos y se aplica con la 00004; las tablas sin `space_id` usan políticas por comando y la escritura sin lectura posterior). 2. Pruebas rojas. 3. `model`, 00004, 00005. 4. `state`. 5. Docs, `task lint`, `task test`, `task docs:check`. Commits: `docs(adr)`, `test(kernel)`, `feat(kernel)`, `docs`.

## Riesgos técnicos y mitigación
- **Contradicciones documentales (sin bloqueo).** (a) ADR-0027 y el diseño de spaces-03 describen la política por `space_id`; `identities` no tiene esa columna: se resuelve con la enmienda, sin cambiar la decisión. (b) El criterio 6 ("crear uno con `space_id = A`") es inalcanzable por la API Go, que fija `space_id`; se prueba con SQL directo. (c) `kernel.md` no menciona el índice único por `(space_id, identity_id)`; la historia lo marca como regla nueva. Recomiendo aceptarla.
- **Existencia revelada.** El único `(provider, subject)` y la clave foránea revelan que una identidad existe en otro espacio (inherente al criterio 1). Un espacio que conozca el UUID de una identidad puede crear un participante con ella y leer su correo; UUID v7 con 74 bits aleatorios no se adivina y B3 debe vincular por invitación verificada. Revisor de seguridad: confirmar.
- **Buscar o crear identidad al iniciar sesión (B3).** Bajo la RLS no se puede leer una identidad por `(provider, subject)` sin participante. Eso exigirá una vía acotada propia con su ADR; no se resuelve aquí.
- **Identidad huérfana** si falla la creación del participante posterior: invisible e inofensiva; la reutilizará el siguiente intento por su `rule_violation` en B3.
- **Guarda N1 en entornos existentes** puede frenar un despliegue: es lo buscado; documentar el `ALTER ROLE` correctivo en la enmienda.
- **Prueba N1 con `ALTER ROLE` transaccional** toma un bloqueo breve sobre el rol del clúster; transacción corta y siempre `ROLLBACK`.
- Supuestos nuevos para objetar en el PR: `display_name` obligatorio en ambas tablas, par tipo/número de documento, formato de `country_code`, `UPDATE` por columnas.

## Fuera de alcance
`contact_points`, `invitations`, `api_keys`, token de Clerk (B3), `last_seen_at` y `disabled_at`, funciones de lectura o listado, reactivar o editar participantes, eventos, verificación de permisos, buscar o crear identidad por proveedor, edición de la 00001.
