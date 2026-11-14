# ADR-0027 · Roles de base de datos y RLS con SET LOCAL

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

El aislamiento entre espacios se apoya en la seguridad por filas (RLS) de PostgreSQL (`modelo-de-datos/kernel.md`, Aislamiento). PostgreSQL no aplica la RLS a los superusuarios, a los roles con `BYPASSRLS` ni, por defecto, al dueño de la tabla. Si la API se conectara con cualquiera de ellos, la política no valdría nada. Además, el espacio actual debe fijarse por transacción y no filtrarse entre transacciones de una misma conexión reutilizada por el pool (ADR-0019 permite conexión directa o pooler en modo sesión).

## Decisión

Hay dos roles. El dueño de las migraciones es el rol de `DATABASE_URL`; no se crea. El rol de la API, `nexusthrive_api`, se crea en la migración 00001 sin superusuario, sin `BYPASSRLS`, sin `CREATEDB`, sin `CREATEROLE` y sin ser dueño de ningún objeto; nace `NOLOGIN`. Si ya existe con `rolsuper` o `rolbypassrls`, la migración falla.

Toda tabla con datos de espacio activa la RLS con `ENABLE` y `FORCE`, con una política sin cláusula `TO` que compara con `kernel.current_space_id()`. Esa función lee el parámetro `kernel.space_id` con `nullif(current_setting('kernel.space_id', true), '')::uuid`: sin valor no hay filas ni error. El parámetro se fija por transacción con `set_config('kernel.space_id', $1, true)` (equivalente parametrizado de `SET LOCAL`). Los permisos se otorgan por tabla, de forma explícita, sin `ALTER DEFAULT PRIVILEGES`; el rol de la API nunca recibe `DELETE`, `TRUNCATE` ni acceso a `goose_db_version`.

La contraseña nunca está en el repositorio ni en una migración. Tras migrar, `kernel/db/cmd/migrate` lee `API_DB_PASSWORD` y, si no está vacía, ejecuta `ALTER ROLE nexusthrive_api LOGIN PASSWORD ...`. Si está vacía, el rol sigue `NOLOGIN`. Las migraciones nunca usan `ALTER ROLE`, porque los roles son del clúster y las bases de prueba paralelas competirían.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Conectar con el dueño de las tablas y filtrar en la aplicación | Un olvido en una consulta expone datos de otro espacio; la base no lo impediría. |
| RLS sin `FORCE` | El dueño de la tabla se saltaría la política. |
| `SET` de sesión en lugar de transacción | El valor sobreviviría a la transacción y se filtraría al siguiente uso de la conexión. |
| Esquema o base de datos por espacio | Multiplica migraciones y conexiones; no escala con miles de espacios. |
| Contraseña en una migración | Quedaría en el repositorio y en el historial de despliegues. |

## Consecuencias

### Positivas

- La base misma impide que la API lea o escriba datos de otro espacio, aunque una consulta olvide filtrar.
- Un patrón único (función, política, permisos explícitos) que copian todas las tablas siguientes.
- Sin secretos en el repositorio.

### Negativas

- Las pruebas y la API deben usar un rol sin privilegios; el administrador de pruebas (superusuario) ve todo y solo sirve para preparar datos.
- Cada tabla nueva exige declarar sus permisos.

### Riesgos

- El pooler en modo transacción rompería `set_config(..., true)` y el aislamiento; está prohibido (ADR-0019).
- `ALTER ROLE ... PASSWORD` puede quedar en los registros del servidor si registra DDL; alternativa futura: enviar un verificador SCRAM calculado en Go.
- Las pruebas usan `SET ROLE` y no ejercitan el inicio de sesión real; B6 añade una prueba con `API_DATABASE_URL`.

## Cumplimiento

Pruebas de integración contra PostgreSQL real: `pg_roles` del rol de la API, ausencia de propiedad de objetos, `relforcerowsecurity`, ausencia de filas sin espacio, no filtración entre transacciones y rechazo de escrituras a otro espacio.

## Enmienda 2026-11-13: endurecimiento y tablas sin `space_id`

Esta enmienda no cambia la decisión ni el estado de la ADR; precisa cómo se aplica a las tablas siguientes.

- **`search_path` fijo.** `kernel.current_space_id()` declara `SET search_path = pg_catalog, pg_temp`. Sin ello, una función homónima de `current_setting` en un esquema anterior de la ruta podría suplantar el espacio. La migración 00004 la reemplaza con `CREATE OR REPLACE`, que conserva el `GRANT EXECUTE`.
- **Guarda del rol.** La migración 00001 solo rechazaba `rolsuper` y `rolbypassrls`. La 00004 comprueba cinco atributos: `rolsuper`, `rolbypassrls`, `rolcreaterole`, `rolcreatedb` y `rolreplication`. Es una migración y no una función de verificación porque corre en todo despliegue, en bases nuevas y en las que ya aplicaron la 00001. Si un entorno tiene el rol con alguno de esos atributos, la migración falla y el despliegue se detiene; el operador lo corrige con `ALTER ROLE nexusthrive_api NOCREATEROLE NOCREATEDB NOREPLICATION`.
- **Valor malformado.** Un valor no vacío y mal formado en `kernel.space_id` sigue fallando con `22P02` (cierra por defecto); el vacío cuenta como sin valor.
- **Tablas sin `space_id`.** `identities` no pertenece a un espacio. Usa políticas por comando: `SELECT` solo cuando existe un participante de esa identidad en el espacio actual, e `INSERT` con `WITH CHECK (true)`. No hay políticas ni permisos de `UPDATE` o `DELETE`. Las tablas con `space_id` siguen comparando con `(SELECT kernel.current_space_id())`, que se evalúa una vez por consulta.
- **Escritura sin lectura posterior.** `INSERT ... RETURNING` y `ON CONFLICT` exigen que la fila pase la política `SELECT`, y una identidad sin participante no la pasa. Por eso la identidad se inserta con un id generado en Go, sin `RETURNING` ni `ON CONFLICT`. No se usa `SECURITY DEFINER`: con `FORCE`, la política rige también al dueño no superusuario, y una función privilegiada se comportaría distinto según el entorno y añadiría superficie de escalada.

### Riesgo aceptado en M1: vincular una identidad ajena

La clave foránea ignora la RLS, y la política de `INSERT` de `identities` no limita quién crea una identidad. Un espacio que conozca el UUID de una identidad de otro espacio podría crear un participante vinculado a ella y, a partir de ahí, leer su correo y su nombre por la política de visibilidad. Es aceptable en M1 porque solo el actor `system` crea participantes (cualquier otro actor recibe `permission_denied`). Un UUID no es un secreto y no se debe tratar como barrera: lo que sostiene el riesgo es que el `IdentityID` nunca viene de la petición de un cliente, sino que lo resuelve el servidor (invitación verificada o sesión). La prueba `TestIdentityOfAnotherSpaceCanBeLinked` de `kernel/laws` fija el comportamiento aceptado. La fase B3 exigirá una vía acotada para vincular identidades (por ejemplo, invitación verificada) y para buscar o crear una identidad por proveedor al iniciar sesión, cada una con su propia ADR.

## Referencias

- `docs/02-arquitectura/modelo-de-datos/kernel.md`
- ADR-0010 (esquemas por capa), ADR-0019 (alojamiento y entornos), ADR-0024 (migraciones)
- `docs/ingenieria/diseno/HU-kernel-spaces-03.md`
