# Diseño: HU-kernel-spaces-03

## Resumen
Se crea `kernel.spaces` con RLS forzada, el rol de base de datos `nexusthrive_api` (sin superusuario, sin `BYPASSRLS`, sin ser dueño) y el helper `state.InSpace`, que abre la transacción y fija `kernel.space_id` con `set_config(..., true)`. `state.CreateSpace` es la operación interna de arranque con actor `system`. El patrón (función `kernel.current_space_id()`, política, grants explícitos) es el que copiarán todas las tablas siguientes.

## Tipo de trabajo
`backend`: SQL, Go y arnés de pruebas; no hay pantallas ni cliente.

## Decisiones (justificadas)
1. **Dos roles.** Dueño de migraciones: el rol de `DATABASE_URL` (`nexusthrive` en local y CI, `postgres` en Supabase); no se crea. Rol de la API: `nexusthrive_api`, creado por la migración 00001 si falta, con `NOLOGIN` y sin atributos peligrosos. Si ya existe con `rolsuper` o `rolbypassrls`, la migración falla (cierra por defecto).
2. **Sin superusuario.** `CREATE ROLE` solo exige `CREATEROLE` (lo tiene `postgres` en Supabase). Si un proveedor lo prohíbe, el operador crea el rol antes y la migración lo respeta. Nunca `ALTER ROLE` en migraciones (evita carreras entre bases de prueba paralelas, pues los roles son del clúster); la creación captura `duplicate_object` y `unique_violation`.
3. **Contraseñas.** Nunca en el repositorio. `kernel/db/cmd/migrate`, tras migrar, lee `API_DB_PASSWORD`; si no está vacía ejecuta `ALTER ROLE nexusthrive_api LOGIN PASSWORD <literal>` (el literal se obtiene con `SELECT quote_literal($1)`, porque DDL no admite parámetros). Si está vacía, el rol sigue `NOLOGIN`. Local, CI y pruebas no la necesitan. `.env.example` documenta `API_DB_PASSWORD=` y `API_DATABASE_URL=` sin valores reales.
4. **Grants explícitos por tabla**, sin `ALTER DEFAULT PRIVILEGES`: `USAGE` en el esquema; en `spaces` solo `SELECT, INSERT, UPDATE` (nunca `DELETE`, `TRUNCATE` ni acceso a `goose_db_version`).
5. **RLS con `ENABLE` y `FORCE`**, política sin cláusula `TO`: aplica igual al dueño no superusuario. Superusuarios y `BYPASSRLS` la saltan siempre; por eso el rol de la API no lo es y el arnés usa el superusuario solo como administrador.
6. **Valor vacío.** Tras un `COMMIT`, el parámetro queda como cadena vacía en la sesión; `''::uuid` lanzaría error. La función usa `nullif(current_setting('kernel.space_id', true), '')::uuid`: sin valor, 0 filas y sin error. Un valor no vacío mal formado falla (cierra por defecto).
7. **Paquetes** (alineado con `plan.md`, "Módulos compartidos"): `kernel/model` (hoja: errores tipados, actor, tipos de espacio) y `kernel/state` (helper y `CreateSpace`). `execution` (HU-13) envolverá el helper. `database/sql` con pgx stdlib, igual que `db.Migrate` y `dbtest`; SQL escrito a mano (sqlc llega cuando haya volumen).
8. **ADR antes del código.** Nuevo `docs/05-decisiones/0027-roles-y-rls.md` (0026 está reservado a idempotencia), Estado Aceptada, primer commit de la rama: roles, `FORCE`, contraseña fuera de banda. Enlazarlo en `docs/05-decisiones/README.md`.

## Módulos a tocar
- `kernel/db/dbtest/dbtest.go`: añade `APIPool` (ver Contrato). `Database` no cambia.
- `kernel/db/cmd/migrate/main.go`: aplica la contraseña si `API_DB_PASSWORD` está definida.
- `kernel/architecture_test.go`: añade `TestMigrationsDoNotLearnBusinessVocabulary` (escanea `kernel/db/migrations/*.sql` con `forbiddenWords`); no se tocan las pruebas actuales.
- `kernel/go.mod` y `go.sum`: `github.com/google/uuid v1.6.0` directa (ya en `go.sum`; `NewV7`).
- `.env.example`, `docs/07-ingenieria/testing.md` (el arnés corre como `nexusthrive_api`), `docs/02-arquitectura/modelo-de-datos/kernel.md` (Aislamiento: rol, `FORCE`, nombre del parámetro), `docs/05-decisiones/README.md`.

## Archivos nuevos
- `kernel/db/migrations/00001_api_role.sql`: crea el rol y `GRANT USAGE ON SCHEMA kernel`.
- `kernel/db/migrations/00002_current_space.sql`: función `kernel.current_space_id()`.
- `kernel/db/migrations/00003_spaces.sql`: tabla, restricciones, RLS, política, grants.
- `kernel/db/roles.go`: `const APIRole = "nexusthrive_api"` y `SetAPIRolePassword(ctx, db, password)`.
- `kernel/model/errors.go`: `Code`, `Error`, constructores y `CodeOf(err)`.
- `kernel/model/space.go`: `SpaceKind`, `SpaceStatus`, `Actor`, `ActorKind`.
- `kernel/state/transaction.go`: `InSpace`. `kernel/state/space.go`: `CreateSpace`.
- Pruebas: ver más abajo.

## Cambios de datos
SQL propuesto (cada archivo con `-- +goose Up`; el DO lleva `StatementBegin/End`):
```sql
-- 00001_api_role.sql
DO $$ BEGIN
  CREATE ROLE nexusthrive_api NOLOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION;
EXCEPTION WHEN duplicate_object OR unique_violation THEN NULL;
END $$;
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'nexusthrive_api' AND (rolsuper OR rolbypassrls)) THEN
    RAISE EXCEPTION 'role nexusthrive_api must not be superuser nor bypass RLS';
  END IF;
END $$;
GRANT USAGE ON SCHEMA kernel TO nexusthrive_api;

-- 00002_current_space.sql
CREATE FUNCTION kernel.current_space_id() RETURNS uuid LANGUAGE sql STABLE AS
$$ SELECT nullif(current_setting('kernel.space_id', true), '')::uuid $$;

-- 00003_spaces.sql
CREATE TABLE kernel.spaces (
  id uuid PRIMARY KEY CHECK (substr(id::text, 15, 1) = '7'),
  kind text NOT NULL CHECK (kind IN ('platform','business','community','personal')),
  name text NOT NULL CHECK (btrim(name) <> ''),
  slug text NOT NULL UNIQUE,
  status text NOT NULL DEFAULT 'provisioning'
    CHECK (status IN ('provisioning','active','suspended','closed')),
  parent_space_id uuid REFERENCES kernel.spaces (id) CHECK (parent_space_id <> id),
  locale text NOT NULL DEFAULT 'es-CO',
  timezone text NOT NULL DEFAULT 'America/Bogota',
  currency text NOT NULL DEFAULT 'COP',
  created_at timestamptz NOT NULL DEFAULT now(),
  suspended_at timestamptz, suspended_reason text, closed_at timestamptz);
ALTER TABLE kernel.spaces ENABLE ROW LEVEL SECURITY;
ALTER TABLE kernel.spaces FORCE ROW LEVEL SECURITY;
CREATE POLICY spaces_isolation ON kernel.spaces
  USING (id = kernel.current_space_id()) WITH CHECK (id = kernel.current_space_id());
GRANT SELECT, INSERT, UPDATE ON kernel.spaces TO nexusthrive_api;
```
Las tablas siguientes usarán `USING (space_id = (SELECT kernel.current_space_id()))` (subconsulta para que se evalúe una vez por consulta).

## Contrato
```go
// kernel/model
type Code string // CodeInvalidInput = "invalid_input", CodeRuleViolation = "rule_violation", CodePermissionDenied = "permission_denied"
type Error struct{ Code Code; Message string; Err error } // Error(), Unwrap()
func CodeOf(err error) (Code, bool)
type ActorKind string // ActorSystem = "system"
type Actor struct{ Kind ActorKind }

// kernel/state
const SpaceSetting = "kernel.space_id"
func InSpace(ctx context.Context, pool *sql.DB, spaceID string, fn func(ctx context.Context, tx *sql.Tx) error) error
type NewSpace struct{ Kind model.SpaceKind; Name, Slug string }
func CreateSpace(ctx context.Context, pool *sql.DB, actor model.Actor, in NewSpace) (spaceID string, err error)

// kernel/db/dbtest
func APIPool(t testing.TB, admin *sql.DB) *sql.DB
```
- `InSpace`: `spaceID` vacío, no UUID o `uuid.Nil` devuelve `invalid_input` sin llamar a `BeginTx`. Luego `BeginTx`, `SELECT set_config('kernel.space_id', $1, true)` (parametrizado), `fn`, `Commit`. Ante error o pánico hace `Rollback` (el pánico se relanza). Errores con `fmt.Errorf("...: %w", err)`.
- `CreateSpace`: actor distinto de `system` devuelve `permission_denied` (adición del diseño; no está en los criterios). Valida `kind` y `name` no vacío (`invalid_input`) y `slug` con `^[a-z0-9]+(-[a-z0-9]+)*$` y máximo 63 caracteres (supuesto A7, ver Riesgos). Genera `uuid.NewV7()` en Go y llama `InSpace(newID)`: el `INSERT` (sin `parent_space_id`, que queda fuera de alcance) pasa `WITH CHECK` porque `id` es el espacio actual. SQLSTATE `23505` se traduce a `rule_violation`; `23514` e `22P02` a `invalid_input`. El `status` es el valor por defecto `provisioning`.
- `APIPool`: obtiene la base con `SELECT current_database()` sobre `admin`, abre un pool pgx stdlib con `stdlib.OptionAfterConnect` que ejecuta `SET ROLE nexusthrive_api`, `MaxOpenConns(4)` y `t.Cleanup` para cerrarlo (corre antes del `DROP DATABASE`).

## Pruebas requeridas (escribirlas primero y verlas fallar)
Todas con `dbtest.Database(t)` (administrador, superusuario, ve todo) y `dbtest.APIPool` (rol limitado). Los datos de prueba usan `test-space-a`, `test-space-b`.
- `kernel/db/spaces_schema_test.go`: `TestSpacesTableShape` (criterio 1: columnas y tipos en `information_schema`, `slug` único, `CHECK` de `kind` y `status`, valores por defecto, `relrowsecurity` y `relforcerowsecurity` verdaderos); `TestAPIRoleIsLimited` (criterio 2: `pg_roles` con `rolbypassrls=false`, `rolsuper=false`, `rolcreaterole=false`; ninguna relación ni función del esquema `kernel` tiene como dueño a `nexusthrive_api`; el dueño de `spaces` es distinto; sin `CREATE` en el esquema, sin `DELETE`/`TRUNCATE` en `spaces` y sin acceso a `goose_db_version`); `TestAPIRoleNameMatchesConstant`; `TestMigrationsAreIdempotentWithExistingRole` (migrar una segunda base del mismo clúster no falla).
- `kernel/state/transaction_test.go`: `TestInSpaceRejectsInvalidSpaceID` (criterio 8: `""`, `uuid.Nil`, `"abc"` dan `invalid_input`; un `pool` cerrado demuestra que no se abrió transacción, pues un `BeginTx` habría devuelto otro error); `TestNoSpaceSeesNoRows` (criterio 4: consulta fuera de `InSpace`, 0 filas, sin error, con 2 espacios insertados por el administrador); `TestSpaceDoesNotLeakAcrossTransactions` (criterio 6: `SetMaxOpenConns(1)`; primera transacción con A y `pg_backend_pid()`, `COMMIT`, luego consulta sin espacio: 0 filas, mismo pid, `current_setting('kernel.space_id', true)` vacío o nulo); `TestInSpaceRollsBackOnErrorAndPanic`.
- `kernel/state/space_test.go`: `TestCreateSpaceVisibleOnlyInOwnSpace` (criterio 3: A y B por `CreateSpace`; con `InSpace(A)` solo A; una variante con `BEGIN` y `SET LOCAL kernel.space_id = '<A>'` literal ve lo mismo); `TestRLSRejectsWritesToOtherSpace` (criterio 5: `INSERT` con `id = B` y `UPDATE ... SET id = B` sobre A fallan con SQLSTATE `42501`; `UPDATE ... WHERE id = B` afecta 0 filas, porque la política lo oculta, ver Riesgos); `TestCreateSpaceCreatesProvisioningSpace` (criterio 7: devuelve UUID versión 7, `status = provisioning`, valores por defecto); `TestCreateSpaceDuplicateSlug` (`rule_violation`, el administrador cuenta una sola fila); `TestCreateSpaceUnknownKind` (`invalid_input`, 0 filas); `TestCreateSpaceRejectsNonSystemActor`.
- `kernel/db/dbtest/dbtest_test.go`: `TestAPIPoolRunsAsAPIRole` (`current_user` es `nexusthrive_api`).
- `kernel/architecture_test.go`: `TestMigrationsDoNotLearnBusinessVocabulary` y las existentes en verde.

## Orden de trabajo
1. ADR-0027 (commit propio). 2. Pruebas (fallan a compilar o ejecutar). 3. `model`, migraciones y `roles.go`. 4. `state` y `APIPool`. 5. `cmd/migrate`, `.env.example`, documentación y `task lint`, `task test`, `task docs:check`. Commits: `docs(adr)`, `test(kernel)`, `feat(kernel)`, `docs`.

## Riesgos técnicos y mitigación
- **Criterio 5 y UPDATE.** Una actualización de una fila de otro espacio no da error: la fila es invisible (0 filas). Solo el cambio de `id` hacia otro espacio lo rechaza con `42501`. Se prueban las tres formas; recomiendo aceptar esa lectura del criterio sin cambiarlo.
- **Documentación.** `modelo-de-datos/kernel.md` no nombra el parámetro ni el rol; se completa en esta historia. Sin otras contradicciones con la historia; no está bloqueada.
- **SET ROLE en pruebas** no ejercita el inicio de sesión real ni `CONNECT`. Mitigación: el rol no tiene privilegios que dependan del login; B6 añade una prueba con `API_DATABASE_URL`.
- **Administrador sin `BYPASSRLS`:** las cuentas del arnés verían 0 filas por `FORCE`. Local y CI usan superusuario; documentarlo en `testing.md`.
- **Fuga de existencia por `slug`:** el índice único detecta slugs de otros espacios y revela que existen. Es deseado (el slug es global).
- **Contraseña en registros** si el servidor registra DDL. Aceptado ahora; alternativa futura: enviar un verificador SCRAM calculado en Go.
- **Carrera al crear el rol** entre bases de prueba paralelas: cubierta por la captura de excepciones y por una prueba que migra varias bases en paralelo.
- **Pooler:** `set_config(..., true)` exige sesión o conexión directa (ADR-0019); nunca modo transacción.
- **Supuesto nuevo A7:** formato de `slug` y `name` no vacío no están en la historia; el responsable puede objetarlos. El valor `business` de `kind` está en la documentación del modelo; no choca con la prueba de arquitectura (sus palabras prohibidas son otras). Revisado a mano: ni el SQL ni las cadenas usan `customer`, `invoice`, `sale`, `membership`, `gym` ni `restaurant`.
- **Ruido de numeración:** `00001` a `00003` quedan reservados; las historias siguientes parten de `00004`.

## Fuera de alcance
Sedes (`parent_space_id` sin visibilidad jerárquica ni parámetro en `CreateSpace`), transiciones de `status`, evento `kernel.space.created.v1`, bypass de RLS del staff, creación pública de espacios (B4), `DELETE` de espacios, verificación de permisos (HU-05/06/13), sqlc, conexión real con el rol limitado en CI.
