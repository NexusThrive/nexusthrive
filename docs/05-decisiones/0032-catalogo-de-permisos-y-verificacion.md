# ADR-0032 · Catálogo de permisos y verificación

- **Estado:** Aceptada
- **Fecha:** 2026-11-14
- **Responsables:** santy401

## Contexto

HU-kernel-permissions-06 convierte la ley de permiso (`docs/01-modelo/leyes.md`) en código: ninguna operación se ejecuta sin actor ni permiso. El responsable aprobó el catálogo mínimo de 13 claves. Antes de escribir código (regla 12 de `AGENTS.md`) hay que decidir dónde vive el catálogo, dónde se verifica, en qué orden se devuelven los errores, qué impone la base de datos y cuál es el límite de confianza del actor en M1. ADR-0029 ya fijó las reglas de escalada; este ADR las vuelve comprobaciones concretas.

## Decisión

**Dónde se verifica.** En Go, no en SQL. `kernel/capability` ofrece un `Authorizer` cuyas funciones reciben el `*sql.Tx` que `state.InSpace` ya abrió con el espacio fijado, así la verificación usa el mismo espacio y la misma RLS que la operación (no hay parámetro de espacio: un id de otro espacio no se ve). La vigencia se calcula con `model.Grant.IsActive(clock.Now())`, la única fuente de verdad; el reloj es inyectable. `capability` importa solo `model` y `database/sql`.

**Catálogo.** Las 13 claves viven en Go (`model.KernelCatalog()`) con su riesgo (`read`, `write`, `money`) y la marca `PlatformOnly`. No hay tabla de catálogo en SQL: un arreglo no admite clave foránea y exigiría que el rol de la API o una migración registre claves de distribuciones. Las distribuciones amplían con `Catalog.Extend(namespace, defs)`, que devuelve un catálogo nuevo (sin estado global): el espacio de nombres no puede ser `kernel`, cada clave empieza por `namespace.`, sin duplicados, riesgo válido y `PlatformOnly` falso. SQL conserva solo el formato (`kernel.valid_permission_list`) y la lista fija de permisos solo de plataforma.

**Orden de evaluación** (se corta en el primer fallo):

1. Actor nulo, mal formado o no resoluble: `permission_denied`, aunque la clave sea desconocida (un anónimo no obtiene información).
2. Clave de permiso o de capacidad fuera del catálogo o mal formada: `invalid_input` (error de código).
3. Sin grant vigente, vencido, revocado o con participante archivado: `permission_denied` con un único mensaje.
4. Espacio `suspended` o `closed` con riesgo `write` o `money`: `space_suspended`. `provisioning` cuenta como `active`.
5. Capacidad declarada y no `active` en el espacio: `capability_inactive`.

Permiso antes que estado, para que quien no tiene permiso no sepa que el espacio está suspendido. El actor `system` salta el paso 3, no los demás. Actor nulo es `Actor{}` o un actor que no es `system` sin participante resoluble; `system` sin participante no es nulo. Los participantes archivados pierden sus permisos.

**Concurrencia.** Para `write` y `money`, `Check` toma `FOR SHARE` sobre los grants leídos y sobre la fila del espacio: una revocación o suspensión concurrente espera a que termine la transacción, y no se comprueba con un grant ya revocado. `read` no bloquea.

**Escalada** (ADR-0029): `CheckRoleCreatable`, `CheckGrantable`, `CheckRevocable` y `CheckCapabilityEnable` implementan las reglas 1 a 5; el cableado en las operaciones es de execution-13. En M1 solo `system` crea roles con `System = true`.

**Lo que impone la base de datos** (migración 00011, sin `SECURITY DEFINER`, con `search_path` fijo): `kernel.capability.write` (la lista fija `kernel.platform_only_permissions()`) no puede estar en un rol ni en `extra_permissions` de un espacio que no sea `platform`; el disparador falla cerrado. Un espacio `closed` no se reabre ni pierde `closed_at`, y el kernel fija `closed_at` con el reloj de la base. Ambos valen para todos los escritores, incluido el dueño de las tablas. Quién es el actor de plataforma lo define B4.

**Rol `owner`.** El único rol de sistema del bootstrap, con 10 claves y nunca `kernel.capability.write`, `kernel.timer.process` ni `kernel.outbox.claim` (trabajo de procesos, cumplido por el actor `system` hasta execution-13). Nada en el código trata especial a un rol por su clave o su bandera `system`: no es autoridad.

**Límite de confianza en M1.** El actor lo afirma la capa de aplicación: `Check` prueba que ese participante tiene permiso, no que quien llama sea ese participante. El rol de la API puede escribir `grants` directamente. B3 construye el `Actor` solo desde un token validado; B6 revisa si hace falta un actor verificado por la base.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Tabla de catálogo en SQL con clave foránea | Un arreglo no admite clave foránea y obligaría a registrar claves de distribuciones desde la base. |
| Verificación en una función SQL | `now()` no se puede congelar en pruebas y la vigencia ya tiene fuente de verdad en Go. |
| `SECURITY DEFINER` para leer grants | Amplía privilegios sin necesidad: la RLS del espacio de la transacción basta. |
| Parámetro de espacio en `Check` | Permite discrepancia entre el argumento y la transacción. |
| Estado antes que permiso | Revelaría a un anónimo que el espacio está suspendido. |

## Consecuencias

### Positivas

- Un solo punto de verificación, con vigencia, estado y capacidad, bajo la misma transacción y RLS.
- Lo que no admite excepción (`capability.write` fuera de plataforma, reabrir un espacio cerrado) lo impone la base, no la costumbre.

### Negativas

- Las claves de distribuciones guardadas antes de registrarse quedan inertes hasta que se registren.
- Las comprobaciones `Check*` no protegen a quien escribe con el rol de la API sin pasar por ellas.

### Riesgos

- Actor afirmado y no autenticado (seguimientos 28 y 29), hasta B3.
- El rol de la API puede reactivar un espacio `suspended` con `UPDATE (status)`; solo `closed` es terminal.
- `FOR SHARE` exige `UPDATE` y política `UPDATE` en `grants` y `spaces`; una prueba lo verifica con el rol de la API.

## Cumplimiento

Pruebas de `kernel/capability`, `kernel/db` (disparadores y catálogo), `kernel/state` (rol `owner`) y `kernel/laws` (un permiso denegado no deja filas), sin `t.Skip`. La prueba de arquitectura vigila el vocabulario del kernel.

## Referencias

- `docs/ingenieria/diseno/HU-kernel-permissions-06.md`
- ADR-0004 (errores tipados), ADR-0027 (roles y RLS), ADR-0029 (otorgante y escalada)
- `docs/01-modelo/leyes.md`, `docs/02-arquitectura/kernel.md`
