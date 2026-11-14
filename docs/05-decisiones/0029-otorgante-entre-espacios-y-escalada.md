# ADR-0029 · Otorgante entre espacios y reglas contra la escalada de privilegios

- **Estado:** Aceptada
- **Fecha:** 2026-11-14
- **Responsables:** santy401

## Contexto

HU-kernel-grants-05 crea `roles`, `grants`, `space_capabilities` y `access_audit`. Dos problemas exigen una decisión antes del código (regla 12 de `AGENTS.md`):

1. **Referencia del otorgante.** El acceso de soporte (regla 10) lo otorga un participante del espacio `platform` a otro participante de un espacio de cliente. Una clave foránea simple `granted_by -> participants (id)` ignora la RLS y no ata al otorgante a su espacio; una compuesta `(granted_by, space_id)` obligaría a que el otorgante viviera en el mismo espacio que el grant, y haría imposible el soporte. Además, el kernel debe saber de qué `kind` es el espacio del otorgante sin que el cliente pueda mentir.
2. **Escalada de privilegios.** Quien puede otorgar puede, si nada lo frena, otorgarse más de lo que tiene, otorgarse a sí mismo, crear roles del sistema o activar capacidades, que la regla 9 reserva a NexusThrive. En M1 solo el actor `system` escribe, pero las reglas deben existir antes de que permissions-06 y la ejecución (13) abran las operaciones a otros actores.

## Decisión

**Referencia del otorgante.** `grants` guarda `granted_by`, `granted_by_space_id` y `granted_by_space_kind`, los tres nulos o los tres presentes. Dos claves foráneas compuestas los atan a los datos reales: `(granted_by, granted_by_space_id)` hacia `participants (id, space_id)` y `(granted_by_space_id, granted_by_space_kind)` hacia `spaces (id, kind)`, para lo cual `spaces` recibe `UNIQUE (id, kind)`. El cliente no puede declarar un `kind` falso ni un otorgante inexistente. Una restricción de alcance exige que el otorgante sea del mismo espacio del grant o de un espacio `platform`; ningún espacio de negocio otorga en otro. `space_capabilities` usa las mismas columnas (`enabled_by*`) y las mismas reglas.

**Actor de auditoría.** `access_audit` aplica la misma regla de alcance: lleva `actor_space_kind`, atado a `spaces (id, kind)` por una clave compuesta, y un `CHECK` exige que el actor sea del espacio auditado o de un espacio `platform`. Un participante de otro espacio de negocio no puede auditar.

**El `kind` de un espacio no cambia.** Las reglas anteriores dependen del `kind`. El rol de la API solo tiene `UPDATE` por columnas de estado en `spaces` (`name`, `status`, `suspended_at`, `suspended_reason`, `closed_at`; sin `id`, `kind`, `slug`, `created_at` ni `parent_space_id`), y un disparador `kernel.reject_space_kind_change()` rechaza con `restrict_violation` cualquier cambio de `kind`, incluso del dueño o con el permiso concedido por error. **Los espacios `platform` no los crea el rol de la API.** La política de `spaces` acepta cualquier fila cuyo `id` sea el espacio actual, y el rol de la API elige ese valor, así que podía insertar un espacio `platform` nuevo y usarlo como otorgante o actor de auditoría. El `INSERT` del rol pasa a ser por columnas (`id`, `kind`, `name`, `slug`, `parent_space_id`, `locale`, `timezone`, `currency`; el kernel fija `status`, `created_at`, `suspended_*` y `closed_at`) y un disparador `kernel.reject_api_platform_space()` rechaza con `restrict_violation` toda inserción con `kind = 'platform'` cuando `current_user = 'nexusthrive_api'`. Se usa `current_user` y no `pg_has_role(..., 'member')`, porque esta última es verdadera para cualquier superusuario y bloquearía el arranque; `current_user` es el rol efectivo, así que vale con conexión directa del rol y con `SET ROLE`. Los espacios `platform` los aprovisiona el dueño de las migraciones o el administrador. `CreateSpace` con `kind = platform` por el rol de la API devuelve `permission_denied`. Sin lo anterior, una sesión podía ascender su propio espacio a `platform` y otorgarse acceso sin motivo ni vencimiento.

**Acceso de soporte.** Es todo grant cuyo otorgante vive en un espacio `platform` distinto del espacio del grant. Exige `reason` no vacío y `expires_at` no nulo, garantizado por un `CHECK` de la tabla y validado antes en Go con `invalid_input`. El alta de staff dentro del propio espacio `platform` no es soporte y queda libre. Todo grant exige que el otorgante no sea el propio receptor (`CHECK`).

**Reglas de escalada** (obligatorias para permissions-06 y la ejecución):

1. El otorgante debe tener vigente `kernel.grant.write`, y todos los permisos del rol que concede más los `extra_permissions` (un subconjunto de los suyos, nunca más).
2. Nadie se otorga un grant a sí mismo.
3. Solo el actor `system` o el staff de plataforma crean roles con `system = true`.
4. Un rol de espacio nunca habilita capacidades: `space_capabilities` solo se escribe con `source = admin` por staff de plataforma, o con `package`, `ai` o `code` por el kernel (regla 9).
5. Revocar solo lo hace quien podría otorgar.

**Alcance en M1.** Todas las operaciones exigen el actor `system`, así que las reglas 1, 3, 4 y 5 se hacen cumplir cuando permissions-06 y la ejecución lleguen; las reglas 2 y la forma del otorgante ya las hace cumplir la base de datos.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| `granted_by` con clave simple hacia `participants (id)` | La comprobación ignora la RLS: no ata al otorgante a un espacio ni permite validar su `kind`. |
| `granted_by` con clave `(granted_by, space_id)` | El otorgante de soporte vive en otro espacio; haría imposible el soporte. |
| Leer el `kind` del otorgante con un disparador | Añade lógica oculta y una lectura de `spaces` bajo RLS; la clave compuesta lo garantiza de forma declarativa. |
| Dejar la escalada para permissions-06 sin ADR | Las columnas y restricciones de esta historia ya fijan parte de la regla; debe quedar escrita antes. |

## Consecuencias

### Positivas

- El soporte entre espacios es posible y no falsificable en la base de datos.
- Las reglas de escalada están escritas antes de que existan actores distintos de `system`.

### Negativas

- `grants` y `space_capabilities` llevan dos columnas más cada una.
- `spaces.kind` es inmutable (disparador y privilegios por columna); el rol de la API ya no puede actualizar `slug` ni `parent_space_id`, y una historia que lo necesite lo concederá de forma explícita.

### Riesgos

- La API de base de datos puede escribir `system = true` en un rol; en M1 solo llega `system` y permissions-06 lo restringe.
- Un grant vencido pero no revocado bloquea otorgar de nuevo el mismo rol; hay que revocarlo antes.

## Cumplimiento

Pruebas contra PostgreSQL real: inserciones directas como administrador y como API con `kind` falso, otorgante de otro espacio de negocio y soporte sin motivo o sin vencimiento; autootorgamiento, ascenso del propio espacio a `platform` y actor de auditoría de otro espacio de negocio. La prueba de escalada con actores distintos de `system` llega con permissions-06.

## Referencias

- `docs/02-arquitectura/modelo-de-datos/kernel.md`, sección 1 y Reglas
- `AGENTS.md`, reglas 9 y 10
- ADR-0027 (roles y RLS), ADR-0028 (inmutabilidad en dos capas)
- `docs/ingenieria/diseno/HU-kernel-grants-05.md`
