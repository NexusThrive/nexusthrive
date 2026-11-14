# Diseño: HU-kernel-grants-05

## Resumen
Se crean `kernel.roles`, `kernel.grants`, `kernel.space_capabilities` y `kernel.access_audit` con RLS forzada, claves foráneas compuestas, grants por columnas y disparadores que fijan `revoked_at` con `now()` e impiden des-revocar y mutar auditoría. En Go, `kernel/state` ofrece crear rol, otorgar, revocar, consultar vigencia con un reloj inyectable, registrar capacidades y auditoría; solo el actor `system` escribe en M1. Estado: lista, sin bloqueos; hay seis contradicciones con recomendación (abajo).

## Tipo de trabajo
`backend`: solo SQL y Go del kernel; sin contrato público ni app.

## Módulos a tocar
- `kernel/model/errors.go`: añadir `CodeAlreadyDone Code = "already_done"` (ya figura en `contrato.md` y ADR-0004, falta en el código).
- `docs/02-arquitectura/modelo-de-datos/kernel.md`: sección 1 (columnas `created_at`, `granted_by_space_id`, `granted_by_space_kind`, clave de `space_capabilities`), sección 5 (columnas de `access_audit`) y Reglas (ver ADR).
- `docs/product/backlog/nucleo-del-kernel.md`: estado y decisiones de abajo. `docs/ingenieria/sprint-01/seguimientos.md`: seguimientos 1 y 2 de Riesgos.
- `docs/05-decisiones/0029-otorgante-entre-espacios-y-escalada.md` (nuevo, antes del código, regla 12; confirmar que 0029 está libre): referencia de otorgante entre espacios y regla de escalada para permissions-06.

## Archivos nuevos
- `kernel/db/migrations/00008_grants.sql`: cuatro tablas, RLS, grants por columnas, disparadores.
- `kernel/model/grant.go`: `Clock`, `SystemClock`, `Role`, `NewRole`, `Grant`, `NewGrant`, `Grantor`, `(Grant).IsActive(now)`, validaciones puras.
- `kernel/model/capability.go`: `CapabilitySource`, `NewSpaceCapability`, `NewAccessAudit`, `AccessAudit`.
- `kernel/state/role.go`, `grant.go`, `capability.go`, `access_audit.go`: operaciones y traducción de errores (patrón de `account.go`).
- Pruebas: `kernel/model/grant_test.go`, `kernel/db/grants_schema_test.go`, `kernel/state/{role,grant,capability,access_audit}_test.go`.

## Cambios de datos
Migración `00008_grants.sql` (la siguiente libre tras 00007). Primero, objetivos de claves compuestas: `ALTER TABLE kernel.spaces ADD CONSTRAINT spaces_id_kind_key UNIQUE (id, kind)` (el `kind` no puede cambiar mientras algo lo referencie: `ON UPDATE NO ACTION`) y `ALTER TABLE kernel.participants` ya tiene `(id, space_id)`.
```sql
CREATE TABLE kernel.roles (
  id uuid PRIMARY KEY CHECK (substr(id::text,15,1)='7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  key text NOT NULL CHECK (key ~ '^[a-z][a-z0-9_]{0,63}$'),
  name text NOT NULL CHECK (btrim(name) <> ''),
  permissions text[] NOT NULL CONSTRAINT roles_permissions_check CHECK (<perm_list_ok>),
  system boolean NOT NULL,
  CONSTRAINT roles_space_key_key UNIQUE (space_id, key),
  CONSTRAINT roles_id_space_key UNIQUE (id, space_id));
CREATE TABLE kernel.grants (
  id uuid PRIMARY KEY CHECK (substr(id::text,15,1)='7'),
  space_id uuid NOT NULL REFERENCES kernel.spaces (id),
  participant_id uuid NOT NULL, role_id uuid NOT NULL,
  extra_permissions text[] NOT NULL DEFAULT '{}' CHECK (<perm_list_ok>),
  granted_by uuid, granted_by_space_id uuid, granted_by_space_kind text,
  reason text CHECK (reason IS NULL OR btrim(reason) <> ''),
  expires_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  revoked_at timestamptz,
  CONSTRAINT grants_grantor_all_or_none CHECK ((granted_by IS NULL) = (granted_by_space_id IS NULL)
    AND (granted_by IS NULL) = (granted_by_space_kind IS NULL)),
  CONSTRAINT grants_no_self_grant CHECK (granted_by IS NULL OR granted_by <> participant_id),
  CONSTRAINT grants_grantor_scope CHECK (granted_by IS NULL OR granted_by_space_id = space_id
    OR granted_by_space_kind = 'platform'),
  CONSTRAINT grants_support_reason_expiry CHECK (granted_by IS NULL OR granted_by_space_id = space_id
    OR granted_by_space_kind <> 'platform' OR (reason IS NOT NULL AND expires_at IS NOT NULL)),
  CONSTRAINT grants_participant_fkey FOREIGN KEY (participant_id, space_id) REFERENCES kernel.participants (id, space_id),
  CONSTRAINT grants_role_fkey FOREIGN KEY (role_id, space_id) REFERENCES kernel.roles (id, space_id),
  CONSTRAINT grants_granted_by_fkey FOREIGN KEY (granted_by, granted_by_space_id) REFERENCES kernel.participants (id, space_id),
  CONSTRAINT grants_grantor_kind_fkey FOREIGN KEY (granted_by_space_id, granted_by_space_kind) REFERENCES kernel.spaces (id, kind),
  CONSTRAINT grants_id_space_key UNIQUE (id, space_id));
CREATE UNIQUE INDEX grants_active_key ON kernel.grants (space_id, participant_id, role_id) WHERE revoked_at IS NULL;
```
`<perm_list_ok>` = sin nulos (`array_position(x, NULL) IS NULL`), a lo sumo 200 elementos y cada elemento `^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$` (comprobado con `array_to_string(x, ',')` contra la lista completa, o arreglo vacío).
- **`space_capabilities`:** PK `(space_id, capability_key)`; `capability_key` con el patrón de dos o más segmentos; `status` `active|suspended` con `DEFAULT 'active'`; `source` `package|ai|admin|code`; `enabled_by`, `enabled_by_space_id`, `enabled_by_space_kind` con las mismas dos claves compuestas y los mismos CHECK de alcance que el otorgante (staff de plataforma habilita desde su espacio, regla 9); `CHECK (source <> 'admin' OR enabled_by IS NOT NULL)`; `reason` no vacío si existe; `enabled_at timestamptz NOT NULL DEFAULT now()`.
- **`access_audit`:** `id` v7 PK, `space_id` (espacio auditado), `actor_participant_id` NOT NULL con `actor_space_id` y FK compuesta a `participants (id, space_id)`, `action` `read|write`, `subject_ref` opcional, `reason` y `request_id` NOT NULL no vacíos, `grant_id` opcional con FK `(grant_id, space_id)` a `grants`, `occurred_at DEFAULT now()`. Índice `(space_id, occurred_at)`.
- **RLS:** `ENABLE` y `FORCE` en las cuatro; políticas por comando con `(SELECT kernel.current_space_id())`: `roles` y `access_audit` y `space_capabilities` solo `SELECT` e `INSERT`; `grants` `SELECT`, `INSERT` y `UPDATE`.
- **Grants a `nexusthrive_api` (por columnas):** `SELECT` en las cuatro; `INSERT` de `roles` (id, space_id, key, name, permissions, system); `grants` sin `created_at` ni `revoked_at`; `space_capabilities` sin `enabled_at`; `access_audit` sin `occurred_at`; solo `UPDATE (revoked_at)` en `grants`. Nada de `DELETE` ni `TRUNCATE`. `status` de capacidad no se inserta (nace `active`; suspender no está en la historia).
- **Disparador `kernel.guard_grant_update()`** (`BEFORE UPDATE`, por fila, `SET search_path = pg_catalog, pg_temp`): si `OLD.revoked_at IS NOT NULL` o cambia cualquier columna distinta de `revoked_at` (`to_jsonb(NEW) - 'revoked_at' <> to_jsonb(OLD) - 'revoked_at'`) lanza `restrict_violation`; si no, `NEW.revoked_at := now()` cuando es no nulo. Así `revoked_at` no es falsificable (cierra el patrón del seguimiento 19) y no se des-revoca ni siquiera por el dueño. Otro disparador `BEFORE DELETE OR TRUNCATE` en `grants` rechaza con `restrict_violation`.
- **Disparador `kernel.reject_access_audit_change()`** (`BEFORE UPDATE OR DELETE` por fila y `BEFORE TRUNCATE` por sentencia), igual que `events`. Dos capas: sin grant y disparador. `Down` borra todo en orden inverso y `spaces_id_kind_key`.

## Contrato
```go
// model
type Clock interface{ Now() time.Time }; type SystemClock struct{}
type Grantor struct{ ParticipantID, SpaceID string }
type NewGrant struct{ ParticipantID, RoleID string; ExtraPermissions []string
    GrantedBy *Grantor; Reason string; ExpiresAt *time.Time }
func (g Grant) IsActive(now time.Time) bool // revoked_at nil y (expires_at nil o now antes de expires_at)
// state (todas: actor != system -> permission_denied; ids no UUID -> invalid_input)
func CreateRole(ctx, pool, actor, spaceID string, in model.NewRole) (string, error)
func GrantRole(ctx, pool, clock model.Clock, actor, spaceID string, in model.NewGrant) (string, error)
func RevokeGrant(ctx, pool, actor, spaceID, grantID string) error
func GetGrant(ctx, pool, spaceID, grantID string) (model.Grant, error)
func IsGrantActive(ctx, pool, clock model.Clock, spaceID, grantID string) (bool, error)
func EnableCapability(ctx, pool, actor, spaceID string, in model.NewSpaceCapability) error
func RecordAccessAudit(ctx, pool, actor, spaceID string, in model.NewAccessAudit) (string, error)
func ListAccessAudit(ctx, pool, spaceID string, limit int) ([]model.AccessAudit, error)
```
- **Vigencia (criterio 3):** función Go pura sobre la fila leída, no SQL: el reloj es inyectable, y una vista o función con `now()` no se puede congelar en pruebas. `IsGrantActive` lee la fila (`not_found` si no la ve) y llama a `IsActive(clock.Now())`. `GrantRole` rechaza `ExpiresAt <= clock.Now()` con `invalid_input` (no se crean grants ya vencidos; la prueba avanza el reloj). Matiz: `revoked_at` usa el `now()` de la base y la expiración el reloj inyectado; ambos en UTC.
- **Soporte (criterio 4):** `GrantRole` resuelve el espacio del otorgante y su `kind` con `InSpace(grantor.SpaceID)` leyendo `kernel.spaces` (la RLS del espacio destino no lo mostraría) y los inserta en `granted_by_space_*`. Pre-validación Go: si el otorgante es de un espacio `platform` distinto del destino y `Reason` (recortada) es vacía o `ExpiresAt` es nil, `invalid_input`. La garantía real es el CHECK `grants_support_reason_expiry`: el cliente no puede mentir sobre el `kind` porque `grants_grantor_kind_fkey` lo ata a `spaces`, ni usar el propio espacio como otorgante porque `grants_granted_by_fkey` exige que el participante viva allí, y `grants_grantor_scope` impide otorgantes de otro espacio no `platform`. No hace falta disparador ni leer `spaces` bajo RLS. `reason` en grants ordinarios es opcional.
- **Errores:** `23505` (clave de rol, grant activo repetido, capacidad repetida) y FK compuestas `grants_participant_fkey`, `grants_role_fkey`, `grants_granted_by_fkey` o `enabled_by` son `rule_violation`; `23514` y `22P02` son `invalid_input`; FK `*_space_id_fkey` es `not_found`. `RevokeGrant`: `UPDATE ... SET revoked_at = now() WHERE id=$1 AND revoked_at IS NULL`; con 0 filas, si la fila no es visible `not_found`, si lo es `already_done` sin tocar `revoked_at`. Es seguro bajo concurrencia: el segundo `UPDATE` espera el candado, reevalúa el `WHERE` y recibe 0 filas. Se aparta del precedente de `CloseAccount` (devuelve `nil`) porque el criterio 8 lo exige.
- **Escalada (pregunta central):** en M1 un rol de espacio no puede otorgarse nada: todas las operaciones exigen `system`, y la API de base no distingue roles de espacio. La defensa de datos ya presente: `grants_no_self_grant`. Reglas obligatorias para permissions-06 y execution-13 (en el ADR): el otorgante debe tener vigente `kernel.grant.write` y todos los permisos del rol que concede más `extra_permissions` (subconjunto, nunca más); no puede concederse a sí mismo; solo `system` o staff crean roles con `system = true`; `space_capabilities` solo con `source = admin` por staff de plataforma o por `package`, `ai`, `code` del kernel, jamás por un rol de espacio (regla 9); la revocación solo la hace quien podría otorgar.

## Pruebas requeridas
Todas primero y rojas; usan `dbtest.Database` y `dbtest.APIPool`; fallan, no se omiten.
1. Criterio 1: `TestCreateRoleStoresAndRejectsDuplicateKeyPerSpace` (state, `role_test.go`: duplicado `rule_violation`, otro espacio acepta) y `TestRolesCheckConstraints` (db: clave, nombre, permisos mal formados o nulos).
2. Criterio 2: `TestGrantRoleStoresWithNullRevokedAt`, `TestGrantRoleRejectsForeignParticipantOrRole` (otro espacio: `rule_violation` y 0 filas) en `grant_test.go`; `TestGrantsNoSelfGrant`, `TestGrantsOnlyOneActivePerParticipantRole` (db).
3. Criterio 3: `TestGrantIsActiveRespectsExpiryAndRevocation` (model, tabla con reloj falso: sin vencimiento, futuro, igual a ahora, pasado, revocado); `TestIsGrantActiveUsesInjectedClock` (state: avanzar el reloj); `TestGrantRoleRejectsPastExpiry`.
4. Criterio 4: `TestSupportGrantRequiresReasonAndExpiry` (state: otorgante `platform` en otro espacio, sin motivo, motivo en blanco o sin `expires_at` da `invalid_input`; con ambos pasa); `TestGrantsSupportRuleCannotBeBypassed` (db, como administrador y como API: INSERT directo con `granted_by_space_kind = 'business'` para un participante de plataforma falla por la FK; otorgante de otro espacio de negocio falla por `grants_grantor_scope`; sin motivo falla por el CHECK); `TestPlatformGrantWithinOwnSpaceNeedsNoExpiry`.
5. Criterio 5: `TestEnableCapabilityStoresActive`, `TestEnableCapabilityRejectsUnknownSource` (`invalid_input`), `TestEnableCapabilityDuplicateIsRuleViolation`, `TestAdminCapabilityRequiresEnabledBy`; `TestCapabilityEnabledByMustBeSameSpaceOrPlatform` (db).
6. Criterio 6: en `grants_schema_test.go`, `TestAccessAuditRejectsUpdateDeleteTruncateAsAPI` (42501), `...AsAdmin` (`restrict_violation`, tabla intacta), `TestAccessAuditTriggerHoldsEvenIfGranted`; `TestRecordAccessAuditRequiresReasonAndRequestID`.
7. Criterio 7: `TestRolesGrantsCapabilitiesAuditIsolationBetweenSpaces` (state: B lee 0 filas de A y un INSERT con `space_id` de A falla con 42501) y `TestGrantsTablesForcedRLSAndExactPrivileges` (db: ENABLE, FORCE, políticas por comando, `column_privileges` exactos; ningún `INSERT` sobre `revoked_at`, `created_at`, `enabled_at`, `occurred_at`).
8. Criterio 8: `TestRevokeGrantTwiceReturnsAlreadyDoneKeepsRevokedAt` y `TestRevokeGrantConcurrentExactlyOneSucceeds` (state); `TestRevokedAtIsSetByKernelNotClient` (db: API que intenta `revoked_at = '2000-01-01'` obtiene `now()`), `TestRevokedGrantCannotBeUnrevokedOrChanged` (API y administrador, `restrict_violation`), `TestGrantsRejectDelete`.
9. `TestGrantOperationsRequireSystemActor` (`permission_denied`) y `TestRevokeGrantUnknownIsNotFound`. Las pruebas de arquitectura (`adr_test.go`, vocabulario) siguen verdes: usar `test.capability` y `kernel.test.read`, nada de términos de negocio.

## Riesgos técnicos y mitigación
- **Seguimientos nuevos:** (1) la prueba de escalada real y el uso de `access_audit` llegan con permissions-06 y B4; (2) el tope de duración de un acceso de soporte no está definido (solo exige `expires_at`); proponer máximo en B4; (3) `space_capabilities` sin `UPDATE`: suspender o reactivar es otra historia; (4) un grant vencido no revocado bloquea otorgar de nuevo el mismo rol (índice parcial); hay que revocar primero.
- **`kind` de espacio mutable:** la API tiene `UPDATE` sobre `spaces`; la FK `(id, kind)` bloquea el cambio mientras un grant referencie el `kind`, y mutar `kind` debería prohibirse en la historia de espacios (seguimiento).
- **Superusuario:** puede deshabilitar disparadores con DDL deliberado (límite aceptado, igual que `events`).
- **`system = true` en roles:** la API puede escribir el bit; en M1 solo `system` llega; permissions-06 lo restringe.
- **Eventos:** otorgar y revocar no emiten evento aquí; lo añade la ejecución (13) en la misma transacción.
- **Mantenimiento de grants por columnas:** columna nueva no se concede hasta que una migración lo diga; la prueba con `column_privileges` lo detecta.

## Contradicciones historia vs documentación (con recomendación)
1. Criterio 4 literal alcanza cualquier grant cuyo otorgante sea de un espacio `platform`, incluso dentro de ese espacio (staff internos). Recomendación: solo accesos entre espacios (otorgante en otro espacio), que es lo que define "acceso de soporte"; el alta de staff dentro de plataforma queda libre. Confirmar en el PR.
2. Criterios 2 y 4 piden FK compuesta y otorgante de plataforma a la vez, imposibles con `(granted_by, space_id)`. Recomendación: columnas `granted_by_space_id` y `granted_by_space_kind` (y análogas en capacidades), con ADR-0029.
3. `kernel.md` no define `created_at` en grants, clave de `space_capabilities` ni columnas de `access_audit`. Recomendación: las de este diseño y actualizar el documento.
4. Criterio 3 habla de `expires_at` pasado; el kernel no admite crear grants vencidos. Recomendación: probar con reloj avanzado y con filas por SQL de administrador.
5. Criterio 8 pide `already_done` mientras participants-04 y `CloseAccount` repiten en silencio. Recomendación: seguir el criterio; añadir `CodeAlreadyDone` y no tocar los precedentes.
6. Criterio 5 pide registrar `status = active`; la API no inserta `status`. Recomendación: `DEFAULT 'active'`.

## Fuera de alcance
Verificación de permisos y catálogo (permissions-06), eventos de otorgar y revocar (13), actualizar o suspender capacidades, expiración automática por proceso, bypass de RLS del staff y uso real de `access_audit` (B4), `invitations`, `api_keys`, pantallas, tope de duración del soporte.

## Ajustes tras la revisión de calidad

- **`kind` inmutable (H1):** el rol de la API perdió el `UPDATE` de tabla sobre `kernel.spaces` y conserva solo `UPDATE (name, status, suspended_at, suspended_reason, closed_at)`. Un disparador `kernel.reject_space_kind_change()` (`search_path` fijo, `restrict_violation`) impide cambiar `kind` a cualquiera. Cierra la autopromoción a `platform` que anulaba la regla 10. Reemplaza el riesgo "`kind` de espacio mutable" de arriba; el seguimiento 24 queda resuelto. Los cambios van en la migración 00008.
- **Actor de auditoría (H3):** `access_audit` suma `actor_space_kind`, con clave compuesta `(actor_space_id, actor_space_kind)` hacia `spaces (id, kind)` y el `CHECK access_audit_actor_scope` (actor del espacio auditado o de un espacio `platform`). `RecordAccessAudit` lee el `kind` del espacio del actor.
