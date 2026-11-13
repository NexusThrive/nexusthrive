# Diseño: HU-kernel-migrations-01

## Resumen
Se elige goose como herramienta de migraciones en SQL puro y se envuelve en un comando Go del kernel (`kernel/db/cmd/migrate`) que crea el esquema `kernel` y aplica las migraciones; `task db:migrate` es solo un atajo de ese comando. El ADR-0024 se commitea antes de cualquier código.

## Tipo de trabajo
`backend`: Go, SQL, Taskfile y CI; no hay pantallas ni cliente.

## Módulos a tocar
- `Taskfile.yml`: tarea `db:migrate` (`go run github.com/nexusthrive/nexusthrive/kernel/db/cmd/migrate`, con `DATABASE_URL` y valor por defecto igual al de `infra/docker/compose.yaml`). Usa `go run`, igual que `STATICCHECK` y `BUF`, así que no exige instalar goose.
- `.github/workflows/ci.yml`: job `migrations` con servicio `postgres:17` (con `health-cmd pg_isready`); ejecuta `task db:migrate` dos veces y luego `go test ./kernel/db/...`. El job `go` también necesita el servicio y `DATABASE_URL`, porque ejecuta esas pruebas (ver riesgos). Una sola edición a la vez sobre este archivo (plan, módulos compartidos).
- `kernel/go.mod` y `go.work.sum`/`kernel/go.sum`: dependencias `github.com/pressly/goose/v3` y `github.com/jackc/pgx/v5` (adaptador `stdlib`). Fijar versiones exactas.
- `docs/07-ingenieria/development-setup.md`: cómo migrar con y sin `task` (`go run ...` equivalente).
- `docs/05-decisiones/README.md`: añadir ADR-0024 al índice si este lista los ADR.

## Archivos nuevos
- `docs/05-decisiones/0024-herramienta-de-migraciones.md`: ADR (texto abajo). Primer commit de la rama.
- `kernel/db/migrate.go`: `Migrate(ctx, db *sql.DB, cfg Config) error`. Valida el nombre del esquema, ejecuta `CREATE SCHEMA IF NOT EXISTS`, crea el proveedor de goose y aplica `Up`.
- `kernel/db/migrations/` y `kernel/db/embed.go`: directorio de SQL (`NNNNN_<tema>.sql`, `-- +goose Up`) incrustado con `go:embed`, para que el binario de `services/api` migre antes de recibir tráfico (ADR-0019). Sin archivos `.sql` en esta historia; `.gitkeep` con comentario no sirve para `embed`, usar `//go:embed all:migrations`.
- `kernel/db/cmd/migrate/main.go`: lee `DATABASE_URL`, abre la conexión con pgx stdlib, llama `Migrate` con el esquema `kernel`, imprime el archivo que falla y sale con código 1.
- `kernel/db/migrate_test.go`: pruebas de integración (primero).

## Cambios de datos
Esquema `kernel` y su tabla de control `kernel.goose_db_version` (nombre propio por esquema: `<esquema>.goose_db_version`). Ninguna tabla de negocio. El esquema lo crea el código Go, no una migración, porque la tabla de control vive dentro de él y debe existir antes de la primera migración. Nunca se usa `public`.

## Decisión de herramienta
| | goose | golang-migrate | tern | atlas |
|---|---|---|---|---|
| SQL puro | sí | sí | sí | sí, pero modelo declarativo |
| Transacción por archivo | sí, por defecto | no garantizada; deja estado "dirty" | sí | sí |
| Uso como biblioteca Go | sí (`Provider`, `fs.FS`) | sí | sí | limitado |
| Tabla de control por esquema | sí (nombre configurable) | `x-migrations-table` | configurable | propia |
| Ejecutable sin instalar | `go run` | necesita `-tags postgres` | `go run` | binario aparte, licencia mixta |

Se elige **goose**: transacción por archivo (criterio 4), `Provider` con `fs.FS` incrustable y errores con nombre de archivo, bloqueo consultivo opcional y mantenimiento activo. Una tabla de control por esquema permite versionar `kernel`, `platform` y `business` de forma independiente: cada capa tendrá su carpeta (`kernel/db/migrations`, `platform/db/migrations`, ...) y su `<esquema>.goose_db_version`.

Supabase: las migraciones usan la conexión directa o el pooler en modo sesión (ADR-0019); nunca el modo transacción. Con modo sesión se puede usar el bloqueo de sesión de goose; con conexión directa también. Documentarlo en el ADR.

## Contrato
```go
// Config selects the schema and the migration files to apply.
type Config struct {
	Schema string  // validated against ^[a-z_][a-z0-9_]*$ and quoted with pgx.Identifier
	Files  fs.FS   // goose SQL files; embedded kernel migrations by default
}
func Migrate(ctx context.Context, db *sql.DB, cfg Config) error
```
- Esquema inválido o `DATABASE_URL` vacío: error antes de tocar la base.
- Sin archivos SQL (`goose.ErrNoMigrations`): éxito; el esquema y la tabla de control igual quedan creados (verificar contra la versión fijada).
- Repetir la orden: sin cambios, código 0. SQL inválido: error `migrate <archivo>: %w`, código distinto de 0, sin cambios parciales.
- `task db:migrate`: código 0 o distinto de 0 es el único contrato de la CLI.

## Pruebas requeridas
Todas en `kernel/db/migrate_test.go`; escribirlas y verlas fallar antes de `migrate.go`. Cada prueba usa un esquema aleatorio `kernel_test_<id>` que elimina al terminar y falla (no `t.Skip`) si no hay PostgreSQL, indicando `task infra:up`.
1. Criterio 1 (ADR): `TestADR0024Exists`: el archivo existe, dice `Aceptada`, nombra goose, atlas y golang-migrate. Va en `kernel/db/adr_test.go` o se verifica en revisión; el orden de commits lo exige.
2. Criterio 2: `TestMigrateCreatesSchemaAndVersionTable`: sobre base sin el esquema, tras `Migrate` existen el esquema y `goose_db_version`.
3. Criterio 3: `TestMigrateIsIdempotent`: dos ejecuciones, mismo estado y sin error (con una migración de prueba en `fstest.MapFS`).
4. Criterio 4: `TestMigrateInvalidSQLRollsBack`: un archivo con una sentencia válida seguida de otra inválida; error con el nombre del archivo y la primera sentencia no persistida.
5. Criterio 5: job `migrations` de CI sobre base vacía, dos veces `task db:migrate`; la prueba de integración confirma el mismo estado.
6. `TestMigrateRejectsInvalidSchemaName`: nombres con comillas o mayúsculas se rechazan (evita inyección).
`kernel/architecture_test.go` debe seguir en verde.

## Riesgos técnicos y mitigación
- **`task` puede no estar instalado:** `task db:migrate` solo envuelve `go run`; el comando directo se documenta en `development-setup.md`.
- **El job `go` de CI ejecuta pruebas con base:** sin servicio fallarían y `t.Skip` está prohibido. Se añade el servicio a `go` y a `migrations` ahora; HU-kernel-testinfra-02 lo consolida.
- **`task infra:up` no espera a PostgreSQL:** la primera orden puede fallar por conexión rechazada. Mitigar con reintento corto en el comando o `healthcheck` en compose (decisión del desarrollador, documentarla).
- **Pooler en modo transacción:** rompe sentencias preparadas y bloqueos; el comando lee la URL y advierte en el README, sin validar el modo.
- **Esquema fuera de las migraciones:** `CREATE SCHEMA` en Go es la única excepción a "todo es SQL"; queda explicada en el ADR.
- **Conflictos de numeración de archivos en ramas paralelas:** formato de versión y regla de renumerar en el segundo PR, en el ADR (plan, módulos compartidos).
- **Compatibilidad con la versión anterior** (`ci-cd.md`): migraciones aditivas; se registra como regla en el ADR.
- **Nombre `goose_db_version` por esquema:** confirmar en la versión fijada que goose acepta la tabla con esquema; si no, fijar `search_path` en la conexión y verificarlo con la prueba 2.
- La historia se puede construir tal como está escrita; no está bloqueada.

## Fuera de alcance
Tablas del kernel, migraciones de `platform` y `business`, roles de base de datos (HU-kernel-spaces-03), arnés de pruebas y `testing.md` (HU-kernel-testinfra-02), reversiones (`Down`) en producción, `sqlc`, Redis.

## Texto propuesto del ADR-0024
Archivo `docs/05-decisiones/0024-herramienta-de-migraciones.md`, plantilla de `plantilla.md`:

```markdown
# ADR-0024 · Goose como herramienta de migraciones en SQL puro

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

El kernel necesita crear sus tablas de forma reproducible en local, en CI y en cada despliegue (ADR-0019). Ningún documento fijaba la herramienta ni cómo se versionan las migraciones de los esquemas `kernel`, `platform` y `business` (ADR-0010). Supabase se usa como PostgreSQL administrado, con conexión directa o pooler en modo sesión. Las migraciones deben ser SQL puro, correr en transacción y ejecutarse con una sola orden aunque `task` no esté instalado.

## Decisión

Se usa goose (`pressly/goose` v3) como biblioteca Go, con migraciones en SQL puro (`-- +goose Up`) incrustadas en el binario. Cada capa tiene su carpeta y su tabla de control dentro de su propio esquema (`<esquema>.goose_db_version`). Un comando del kernel crea el esquema si no existe y aplica las migraciones; `task db:migrate` lo invoca con `go run`. Versiones por archivo `NNNNN_<tema>.sql`; si dos ramas reservan el mismo número, la segunda renumera al rebasar. Las migraciones son aditivas y compatibles con la versión anterior del binario. Se ejecutan con conexión directa o pooler en modo sesión, nunca en modo transacción.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| golang-migrate | No garantiza una transacción por archivo y deja estado "dirty" ante un fallo; su CLI exige etiquetas de compilación. |
| Atlas | Modelo declarativo y binario aparte; la historia pide SQL versionado y reproducible, y la licencia de funciones avanzadas añade dependencia comercial. |
| tern | Cumple con SQL puro y transacciones, pero tiene menos uso como biblioteca y menos ecosistema que goose. |

## Consecuencias

### Positivas

- Una transacción por migración y errores que nombran el archivo.
- Una tabla de control por esquema versiona cada capa por separado.
- Sin instalar nada más que Go.

### Negativas

- El esquema se crea en código Go, no en SQL, porque la tabla de control vive dentro de él.
- Una dependencia más en `kernel/go.mod`.

### Riesgos

- Conflictos de numeración entre ramas paralelas.
- Uso accidental del pooler en modo transacción.

## Cumplimiento

- Pruebas de integración de `kernel/db` y un job de CI que migra una base vacía dos veces.
- La revisión de PR rechaza SQL fuera de `migrations/` y migraciones que escriban en otro esquema.

## Referencias

- [ADR-0010](0010-esquemas-por-capa.md)
- [ADR-0019](0019-alojamiento-y-entornos.md)
```
