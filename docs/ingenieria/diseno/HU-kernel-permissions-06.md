# Diseño: HU-kernel-permissions-06

## Resumen
Se crea `kernel/capability` con un `Authorizer` que verifica permiso, estado del espacio y capacidad activa leyendo roles y grants dentro de la transacción que ya fijó el espacio (misma transacción y misma RLS que la operación), más las comprobaciones de escalada del ADR-0029. El catálogo de 13 claves vive en Go (`kernel/model`), la migración 00011 impone en la base los permisos solo de plataforma y que un espacio cerrado no se reabra, y `state` ofrece el bootstrap del rol `owner`. Estado: lista, sin bloqueos; ocho contradicciones con recomendación (abajo) y un ADR nuevo (0032).

## Tipo de trabajo
`backend`: Go y SQL del kernel; sin contrato público ni app.

## Decisiones
- **(a) Dónde vive.** Función Go, no SQL. Recibe `*sql.Tx` ya dentro de `state.InSpace` (execution-13 la llamará antes de escribir, en esa misma transacción), así la RLS de `grants`, `roles`, `participants`, `spaces` y `space_capabilities` limita todo al espacio de la transacción. La vigencia usa `model.Grant.IsActive(clock.Now())`, la única fuente de verdad (reloj inyectable; una función SQL con `now()` no se puede congelar). `capability` importa solo `model` y `database/sql`; `state` no importa `capability`. Las funciones de `state` abren hoy su propia transacción: execution-13 les añade variantes con `tx` (no se toca aquí; regla 11).
- **(b) Catálogo.** Constantes y tabla en Go (`model.KernelCatalog()`), con riesgo y marca `PlatformOnly`. SQL conserva solo el formato (`kernel.valid_permission_list`) y la lista fija de permisos solo de plataforma. No se crea tabla de catálogo en SQL: un arreglo no admite clave foránea y exigiría que el rol de la API o una migración registre claves de distribuciones. Distribuciones: `Catalog.Extend(namespace, defs)` devuelve un catálogo nuevo; el espacio de nombres no puede ser `kernel`, cada clave debe empezar por `namespace.`, sin duplicados, riesgo válido y `PlatformOnly` falso (la base solo conoce las del kernel). El kernel no aprende palabras: las claves llegan por `kernel/extension` desde la distribución (historia posterior). Sin estado global: el `Authorizer` recibe su catálogo.
- **(c) Orden de evaluación** (se corta en el primer fallo): 1) actor nulo, mal formado o no resoluble: `permission_denied` (siempre, aunque la clave sea desconocida: un anónimo no obtiene información); 2) clave de permiso o de capacidad fuera del catálogo o mal formada: `invalid_input` (error de código, antes de leer grants); 3) sin grant vigente: `permission_denied` (mensaje único: no distingue sin grant, vencido, revocado o archivado); 4) estado del espacio: `suspended` o `closed` con riesgo `write` o `money`: `space_suspended`; 5) capacidad declarada y no `active`: `capability_inactive`. Permiso antes que estado, para que quien no tiene permiso no sepa que el espacio está suspendido. El actor `system` salta el paso 3 pero no los demás.
- **(d) Seguimientos.** 20 se resuelve (pruebas de escalada sobre las comprobaciones; el cableado de extremo a extremo es de execution-13). 26 se resuelve en parte: `closed` es terminal (disparador); reactivar un `suspended` lo decide quien certifique al actor, B3/B4. 27 ya está resuelto. 28 y 29 se difieren a B3: en M1 el actor lo afirma la capa de aplicación; `Check` prueba que ese participante tiene permiso, no que quien llama es ese participante. B3: el gateway construye `Actor` solo desde un token validado (Clerk o clave de API) resolviendo el participante por `(space_id, identity_id)`, jamás desde el cuerpo de la petición; execution-13 usa el mismo `Actor` verificado para escribir `actor_participant_id`. 22 sigue abierto (suspender capacidades).
- **(e) Escalada.** Se implementa como funciones `Check*` que execution-13 invoca antes de cada escritura (abajo). Lo que la base ya impone: `grants_no_self_grant`, forma del otorgante y, nuevo, permisos solo de plataforma.
- **(f) Roles de sistema.** Solo `owner` (`system = true`) con 10 claves: `participant.read`, `participant.write`, `role.write`, `grant.write`, `resource.write`, `ledger.read`, `exchange.write`, `commitment.read`, `commitment.write`, `event.read` (todas con prefijo `kernel.`). Nunca `kernel.capability.write`; tampoco `timer.process` ni `outbox.claim` (trabajo de procesos, no de personas; los cumple el actor `system`). Nada en el código trata especial al rol por su `system` o su clave: no es autoridad.

## Módulos a tocar
- `kernel/model/space.go`: comentario de `Actor` (la verificación llega aquí); añadir `Actor.IsNil()` (kind vacío o inválido, o no `system` sin `ParticipantID`).
- `docs/02-arquitectura/kernel.md` y `modelo-de-datos/kernel.md`: catálogo, orden de errores, `owner`, regla `closed` terminal, límite del actor afirmado.
- `docs/product/backlog/nucleo-del-kernel.md` y `docs/ingenieria/sprint-01/seguimientos.md`: estado de la historia y seguimientos 20, 22, 26, 28, 29.

## Archivos nuevos
- `docs/05-decisiones/0032-catalogo-de-permisos-y-verificacion.md` (antes del código, regla 12): decisiones (a) a (f), alternativas (tabla de catálogo en SQL, verificación en función SQL, `SECURITY DEFINER`) y límite del actor afirmado.
- `kernel/model/permission.go`: `Risk`, `PermissionDef`, `Catalog`, `KernelCatalog()`, `Extend`, `ValidateList`, 13 constantes `Permission...`, `OwnerRole()`.
- `kernel/capability/authorizer.go`: `Authorizer` y `Check`. `kernel/capability/escalation.go`: `Check*`.
- `kernel/state/standard_roles.go`: `CreateStandardRoles` (system, una transacción, todo o nada; repetirlo es `rule_violation`).
- `kernel/db/migrations/00011_permission_guards.sql` (siguiente libre).
- `kernel/laws/doc.go` (`package laws`) y `kernel/laws/permission_test.go`: nace el paquete de leyes; laws-15 añade el resto y la prueba de guardia contra `t.Skip`.

## Cambios de datos
Sin tablas ni columnas nuevas; sin privilegios nuevos para `nexusthrive_api`. Migración 00011, todas las funciones con `SET search_path = pg_catalog, pg_temp` y sin `SECURITY DEFINER`:
- `kernel.platform_only_permissions() RETURNS text[] IMMUTABLE` = `ARRAY['kernel.capability.write']`.
- `kernel.guard_platform_only_permissions()` `BEFORE INSERT` por fila en `kernel.roles` (columna `permissions`) y `kernel.grants` (`extra_permissions`): si la lista intersecta (`&&`) la lista de plataforma y `(SELECT kind FROM kernel.spaces WHERE id = NEW.space_id)` es distinto de `'platform'` o nulo, `restrict_violation`. Falla cerrado: sin fila visible no se permite. Roles y grants no tienen `UPDATE` de esas columnas, así que el `INSERT` basta; aplica a todos, también al dueño de las tablas.
- `kernel.guard_space_closed()` `BEFORE UPDATE` en `kernel.spaces` por fila: si `OLD.status = 'closed'` y cambia `status` o `closed_at`, `restrict_violation`; si pasa a `closed`, `NEW.closed_at := now()` (no falsificable, patrón de `revoked_at`).
- `Down` borra disparadores y funciones en orden inverso. Tablas y RLS existentes no cambian (ya `ENABLE` y `FORCE` con `(SELECT kernel.current_space_id())`).

## Contrato
```go
// model
type Risk string // RiskRead, RiskWrite, RiskMoney
type PermissionDef struct{ Key string; Risk Risk; PlatformOnly bool }
func KernelCatalog() Catalog
func (c Catalog) Lookup(key string) (PermissionDef, bool)
func (c Catalog) ValidateList(list []string) error // invalid_input si alguna no esta
func (c Catalog) Extend(namespace string, defs []PermissionDef) (Catalog, error)
func OwnerRole() NewRole
// capability (todas exigen tx con el espacio fijado; sin espacio fijado todo es permission_denied)
type Requirement struct{ Permission, Capability string } // Capability opcional
func NewAuthorizer(c model.Catalog, clock model.Clock) (*Authorizer, error) // reloj nulo: invalid_input
func (a *Authorizer) Check(ctx context.Context, tx *sql.Tx, actor model.Actor, req Requirement) error
func (a *Authorizer) CheckRoleCreatable(ctx, tx, actor, in model.NewRole) error
func (a *Authorizer) CheckGrantable(ctx, tx, actor, in model.NewGrant) error
func (a *Authorizer) CheckRevocable(ctx, tx, actor, grantID string) error
func (a *Authorizer) CheckCapabilityEnable(ctx, tx, actor, in model.NewSpaceCapability) error
// state
func CreateStandardRoles(ctx, pool, actor model.Actor, spaceID string) error
```
- `Check`: consulta única `grants JOIN roles JOIN participants` del participante (todos los grants, sin `LIMIT`: lo acota el índice de grant activo único), filtra con `IsActive(clock.Now())` y exige participante `active`. Las claves nunca se concatenan en SQL; se comparan en Go. Para `write` y `money` toma `FOR SHARE OF g` sobre los grants leídos y `FOR SHARE` sobre la fila del espacio: una revocación o suspensión concurrente espera a que termine la transacción (evita comprobar y usar con un grant ya revocado). `read` no bloquea.
- `CheckRoleCreatable`: `kernel.role.write`; claves en el catálogo (`invalid_input`); permisos del rol incluidos en los efectivos del actor; `System = true` solo por el actor `system` (ADR-0029 regla 3; el staff de plataforma se identifica en B4).
- `CheckGrantable`: `kernel.grant.write`; el rol (visible en el espacio, si no `not_found`) más `ExtraPermissions` incluidos en los efectivos del actor; `ParticipantID` distinto del participante del actor; `GrantedBy` nulo o igual al actor (la atribución no se afirma aparte). El actor `system` solo conserva el último punto.
- `CheckRevocable`: `kernel.grant.write` y los permisos efectivos del grant objetivo incluidos en los del actor (regla 5).
- `CheckCapabilityEnable`: solo `system`, o quien tenga `kernel.capability.write` y `Source = admin`; `package`, `ai` y `code` solo por `system` (regla 9). Un rol de espacio nunca la tiene: lo impide el disparador, no la costumbre.

## Pruebas requeridas
Todas se escriben antes y fallan primero; fallan, nunca se omiten. Verde tras implementar solo si cada pieza (disparador, filtro de vigencia, orden, bloqueo) es necesaria: cada prueba de abajo se comprueba también quitando esa pieza.
1. `kernel/model/permission_test.go`: `TestKernelCatalogHasThirteenKeysWithRisk` (tabla exacta clave-riesgo de la historia), `TestCatalogExtendRules` (rechaza `kernel`, prefijo ajeno, duplicado, riesgo inválido, `PlatformOnly`), `TestCatalogIsImmutable`, `TestOwnerRoleExcludesCapabilityWrite`, `TestActorIsNil`.
2. `kernel/capability/authorizer_test.go` (`dbtest.Database`, `APIPool`, reloj falso):
   - Crit 1 `TestCheckAllowsPermissionFromRole`; crit 2 `TestCheckAllowsPermissionFromExtraPermissions` (y otra clave sigue denegada).
   - Crit 3 `TestCheckDeniesWithoutGrantExpiredRevokedOrArchived` (vence exactamente en `now` denegado; avanzar el reloj; mensaje idéntico en todos).
   - Crit 4 `TestCheckDeniesNilActor` (actor cero, persona sin participante, UUID mal formado, kind desconocido, y clave desconocida con actor nulo sigue `permission_denied`).
   - Crit 5 `TestCheckUnknownKeyIsInvalidInput` (vacía, mal formada, bien formada fuera del catálogo, con actor sin grants).
   - Crit 6 `TestCheckSuspendedAndClosedBlockWriteAndMoney` (recorre las 13 claves según su riesgo; `read` permitido; sin permiso devuelve `permission_denied` y no `space_suspended`).
   - Crit 7 `TestCheckInactiveCapability` (ausente, `suspended` por SQL de administrador, `active`; `space_suspended` precede; clave de capacidad inválida es `invalid_input`).
   - Crit 8 `TestOwnerCannotEnableAdminCapability` (con `CreateStandardRoles` real, `CheckCapabilityEnable` y `Check` dan `permission_denied`).
   - Crit 9 `TestCheckDeniesAcrossSpaces` (actor de A en transacción de B, y viceversa, mismo id de participante) y `TestCheckWithoutSpaceIsDenied`.
   - Concurrencia: `TestCheckHoldsRevocationUntilCommit` (`RevokeGrant` espera al commit de la comprobación; con tiempo límite).
3. `kernel/capability/escalation_test.go`: `TestGrantEscalationRules` (sin `grant.write`, rol con permiso de más, extra de más, autootorgamiento, `GrantedBy` ajeno; permitido: subconjunto; `system` permitido), `TestRevokeNeedsGrantWriteAndSubset`, `TestRoleCreationRules` (`System = true`, clave fuera del catálogo, permisos de más), `TestCapabilityEnableSources`.
4. `kernel/db/permission_guards_test.go`: `TestPlatformOnlyPermissionsRejectedOutsidePlatformSpace` (roles y `extra_permissions`; como API y como administrador; `restrict_violation`, tabla intacta; permitido en un espacio `platform` sembrado por administrador), `TestPlatformOnlyListMatchesCatalog` (cada clave de `KernelCatalog` con `PlatformOnly` está en la función SQL y ninguna otra), `TestClosedSpaceStaysClosed` (API y administrador no reabren ni borran `closed_at`; el `closed_at` lo fija el kernel), `TestGuardFunctionsHaveFixedSearchPathAndNoDefiner` (`pg_proc`).
5. `kernel/state/standard_roles_test.go`: `TestCreateStandardRolesCreatesOwner` (10 claves exactas, `system = true`, repetir es `rule_violation` sin cambios, actor no `system` es `permission_denied`, otro espacio no lo ve).
6. Crit 10 `kernel/laws/permission_test.go`, `TestDeniedPermissionLeavesNoRows`: cuenta con administrador las filas de todas las tablas del esquema `kernel` (lista dinámica de `pg_class`, así una tabla nueva entra sola) antes y después de una operación de prueba que llama a `Check` y luego inserta; denegada (sin grant, vencido, actor nulo, clave desconocida, espacio suspendido, capacidad inactiva): conteos iguales; control positivo con permiso: sube exactamente en 1. Prepara laws-15 sin depender de execution-13.
Orden de implementación: modelo, luego migración con su prueba, `Check`, escalada, `CreateStandardRoles`, `laws`.

## Riesgos técnicos y mitigación
- **Actor afirmado, no autenticado (seguimientos 28 y 29).** Cualquier código del proceso puede construir `Actor{Kind: ActorSystem}`, y el rol de la API puede escribir `grants` directamente: el kernel no puede distinguir al actor en la base. Es el límite de confianza de M1 (solo `system` escribe hoy). Mitigación: las barreras que sí valen para todo escritor (disparadores, CHECK, FK compuestas, RLS) y el ADR 0032; B3 certifica al actor desde el token; B6 revisa si hace falta un actor verificado por la base.
- **Escalada por la base:** un escritor con el rol de la API puede insertar un grant con `granted_by` nulo a sí mismo con permisos del catálogo. Aceptado por lo anterior; lo único que no puede es `kernel.capability.write` fuera de `platform`.
- **Un `suspended` se reactiva con el `UPDATE (status)` del rol de la API.** Solo `closed` queda terminal aquí; el resto necesita actor certificado (B3/B4).
- **Claves inertes de distribuciones:** un rol puede guardar una clave aún no registrada; `Check` la rechaza (`invalid_input`) y solo cuenta cuando la distribución la registre. `CheckRoleCreatable` valida contra el catálogo vivo.
- **Cierre con `FOR SHARE`:** exige `UPDATE` y política `UPDATE` en `grants` y `spaces` (existen). Una prueba lo verifica con el rol de la API real.
- **Ataques considerados:** actor nulo o mal formado; clave desconocida como oráculo; mensaje que distinga vencido de revocado; grant vencido o revocado reutilizado; reloj; lectura entre espacios con el mismo UUID; transacción sin espacio; otorgar `capability.write` por rol, `extra_permissions` o SQL directo; `owner` ampliado después (no hay `UPDATE` de roles); rol `system` falsificado (sin autoridad en el código); autootorgamiento; atribución falsa del otorgante; carrera revocar contra usar; carrera suspender contra escribir; reabrir un espacio cerrado; `search_path` secuestrado en los disparadores; inyección por claves (siempre parámetros).

## Contradicciones historia vs documentación (con recomendación)
1. Criterio 6 solo cubre `suspended`; `closed` y `provisioning` no se mencionan y `contrato.md` no tiene `space_closed`. Recomendación: `closed` se trata como `suspended` para `write` y `money` (mismo código, mensaje distinto) y `provisioning` como `active` (el bootstrap escribe ahí); confirmar en el PR.
2. Criterio 9 habla de verificar "contra el espacio B"; aquí no hay parámetro de espacio: el espacio es el de la transacción, y un id de otro espacio no se ve. Recomendación: mantenerlo, evita la discrepancia entre argumento y transacción.
3. Criterio 4 "actor nulo": el actor `system` sin participante es válido. Recomendación: nulo es `Actor{}` o un actor no `system` sin participante resoluble.
4. Criterio 8 dice "sistema o plataforma", pero no hay forma de reconocer a un actor de plataforma en M1. Recomendación: lo impone la base (solo en espacios `platform`) y B4 define al actor.
5. ADR-0029 regla 3 admite staff para roles `system`; en M1 solo `system`. Recomendación: B4 lo amplía.
6. El criterio 3 y `kernel.md` no dicen si un participante archivado conserva permisos. Recomendación: no (se deniega); añadido como defensa, con prueba.
7. El catálogo marca `kernel.timer.process` y `kernel.outbox.claim` como permisos, y `outbox.go` hoy solo acepta `system`. Recomendación: sin cambio aquí; execution-13 los exigirá.
8. Criterio 10 pide `kernel/laws`, que no existe. Recomendación: crearlo aquí con esa prueba; laws-15 lo completa.

## Fuera de alcance
Cableado de `Check*` en las operaciones y variantes con `tx` (execution-13), idempotencia, autenticación de actores y tokens (B3), actor de plataforma y soporte (B4), suspender capacidades, caché de permisos, expiración por proceso, permisos de plataforma y de distribuciones, tabla de catálogo en SQL, `contracts/`.
