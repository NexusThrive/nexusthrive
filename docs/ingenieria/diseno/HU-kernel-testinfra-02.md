# Diseño: HU-kernel-testinfra-02

## Resumen
Se extrae de `kernel/db/migrate_test.go` un paquete de apoyo `kernel/db/dbtest` que entrega a cualquier prueba una base o un esquema aislado, con migraciones aplicadas, limpieza garantizada y soporte de `t.Parallel()`. Las pruebas de `kernel/db` se refactorizan para usarlo y se actualizan `testing.md` y la hoja de ruta.

## Tipo de trabajo
`backend`: Go, CI y documentación; no hay pantallas ni cliente.

## Estado heredado (no se rehace)
Ya existen por HU-kernel-migrations-01: servicio `postgres:17` en los jobs `go` y `migrations` de `.github/workflows/ci.yml` (con `DATABASE_URL`), puerto local 55432 en `infra/docker/compose.yaml`, esquema `kernel_test_<id>` con `DROP` al terminar y fallo (sin `t.Skip`) con la indicación de `task infra:up`. Criterios 1, 3 y 4 ya se cumplen en esencia; esta historia los cubre con pruebas del arnés y verifica CI sin cambiarlo salvo lo indicado abajo.

## Decisión de ubicación
`kernel/db/dbtest`, no `kernel/internal/testdb`. Un paquete `internal` bajo `kernel/` no es importable desde `platform/` ni `distributions/`, que necesitarán el mismo arnés para sus esquemas (B4, etapa 2). `kernel/db/dbtest` sigue la convención de `net/http/httptest`, es importable por las capas permitidas (apuntan hacia el kernel) y no contiene vocabulario de negocio. Si el desarrollador ve un obstáculo, no cambia la ubicación: lo anota en la PR.

## Módulos a tocar
- `kernel/db/migrate_test.go`: elimina `defaultDatabaseURL`, `openDatabase` y `randomSchema`; usa `dbtest`. Los cuatro tests existentes conservan nombre y aserciones y añaden `t.Parallel()` (los de esquema).
- `docs/07-ingenieria/testing.md`: fila Integración: "PostgreSQL de `task infra:up` en local y servicio `postgres:17` en CI; arnés `kernel/db/dbtest`", sin Testcontainers. Nueva sección en Funcionamiento con las reglas del arnés (abajo) y el aviso de que una prueba de integración sin PostgreSQL falla, nunca se omite.
- `docs/02-arquitectura/hoja-de-ruta.md`: fila B2: añadir "y la prueba de aislamiento de las tablas del kernel de M1 (ningún dato cruza entre espacios)". Fila B6: "Batería completa de pruebas de aislamiento entre espacios (todas las capas), ..." conservando el resto. No tocar el diagrama Mermaid.
- `.github/workflows/ci.yml`: sin cambios previstos. Solo verificar que el job `go` ejecuta `task test:go` con el servicio y `DATABASE_URL`; el nombre de rama se sigue validando. Si se edita, una sola edición a la vez (plan, módulos compartidos).

## Archivos nuevos
- `kernel/db/dbtest/dbtest.go`: el arnés. Importa solo `kernel/db`, `database/sql`, pgx stdlib y la biblioteca estándar.
- `kernel/db/dbtest/dbtest_test.go`: pruebas del propio arnés (primero).

## Cambios de datos
Ninguno permanente. Efímeros: esquemas `kernel_test_<id>` y bases `kernel_test_<id>` creados y eliminados por las pruebas.

## Contrato (API del arnés)
```go
// URL returns DATABASE_URL, or the compose default on port 55432.
func URL() string

// Admin returns a small shared pool on the maintenance database (max 2 conns).
func Admin(t testing.TB) *sql.DB

// Schema creates an empty schema "kernel_test_<id>" in the shared database,
// drops it on cleanup and returns the pool and the name. For tests that
// choose their own schema, such as the migration tests.
func Schema(t testing.TB) (conn *sql.DB, name string)

// Database creates a database "kernel_test_<id>", applies the kernel
// migrations (db.Migrate with Schema "kernel") and returns a pool on it.
// Cleanup closes the pool and runs DROP DATABASE ... WITH (FORCE).
func Database(t testing.TB) *sql.DB

// DatabaseWith is Database with explicit migration files.
func DatabaseWith(t testing.TB, files fs.FS) *sql.DB
```
Reglas de implementación:
- `<id>` = `unixnano` en hexadecimal + 6 bytes de `crypto/rand`; colisión imposible entre procesos de `go test ./...` y entre pruebas paralelas.
- Se valida el nombre con el mismo patrón de `db.Migrate` y se cita con `pgx.Identifier{...}.Sanitize()` antes de construir `CREATE`/`DROP`.
- Conexión: función interna `connect(url string) (*sql.DB, error)` que hace `PingContext` con tiempo límite de 3 s y devuelve un error que contiene `task infra:up` y `DATABASE_URL`. `Admin` lo pasa a `t.Fatalf`. Nunca `t.Skip`.
- Limpieza solo con `t.Cleanup` (corre también tras `t.Fatal`/`FailNow`); un error de `DROP` se reporta con `t.Errorf`. Orden: cerrar el pool de la prueba, luego eliminar.
- Cada `Database` usa su propio pool con `SetMaxOpenConns(4)`; el pool administrativo es único por proceso (`sync.Once`) y se cierra en el último `Cleanup`... o se deja a la salida del proceso (decisión del desarrollador, documentada en un comentario).
- Barrido de huérfanos: la primera llamada de `Admin` por proceso elimina bases y esquemas `kernel_test_*` cuyo prefijo temporal tenga más de una hora (restos de ejecuciones interrumpidas). Nunca toca los recientes, para no interferir con otro paquete en curso.
- `Database` sirve la fidelidad con producción: el esquema se llama `kernel`, así las migraciones pueden calificar `kernel.` sin adaptaciones. `Schema` no ejecuta migraciones.
- Sin `init()` ni estado global visible; sin dependencias de `platform`/`distributions`. Identificadores sin palabras de `forbiddenWords` (`kernel/architecture_test.go` escanea este paquete también).
- Cada función llama `t.Helper()`.

## Orden de pruebas primero
1. Escribir `kernel/db/dbtest/dbtest_test.go` y verlo fallar a compilar/ejecutar.
2. Implementar `dbtest.go` hasta verde.
3. Refactorizar `migrate_test.go`; los cuatro tests siguen verdes sin tocar sus aserciones.
4. Documentación (`testing.md`, hoja de ruta) y `task docs:check`.
Commits sugeridos: `test(kernel): ...`, `feat(kernel): ...`, `refactor(kernel): ...`, `docs: ...` en ese orden.

## Pruebas requeridas
En `kernel/db/dbtest/dbtest_test.go` salvo indicación:
1. Criterio 1: `TestDatabaseAppliesKernelMigrations`: con `Database(t)` existen el esquema `kernel` y `kernel.goose_db_version`. Se amplía cuando lleguen tablas reales; la prueba de `task test` completa es `task test` en local con `task infra:up`.
2. Criterio 2 (limpieza): `TestDatabaseIsDroppedAfterTest` y `TestSchemaIsDroppedAfterTest`: dentro de un subtest se crea el recurso, se anota su nombre; al terminar el subtest, el padre confirma contra `Admin` que `pg_database`/`information_schema.schemata` ya no lo listan. Incluye una variante donde el subtest termina con `t.FailNow` vía `t.Run` con función auxiliar que no propaga el fallo (si no es posible sin fallar el padre, verificarlo con `testing.RunTests` o documentar la limitación).
3. Criterio 2 (paralelo): `TestParallelDatabasesDoNotSeeEachOther`: 8 subtests con `t.Parallel()`, cada uno crea una tabla `probe` y filas distintas en su `Database`; cada uno ve solo sus filas, y los nombres de base son distintos. Lo mismo `TestParallelSchemasAreDistinct` para `Schema`.
4. Criterio 3: `TestConnectErrorMentionsInfraUp`: `connect` contra una URL en un puerto cerrado (`127.0.0.1:1`) devuelve un error que contiene `task infra:up`. `TestURLUsesDatabaseURLEnv`: con `t.Setenv("DATABASE_URL", ...)` se devuelve ese valor; sin ella, el valor por defecto con puerto 55432 (estas dos no usan `t.Parallel()`).
5. `TestNamesAreValidIdentifiers`: los nombres generados cumplen `^[a-z_][a-z0-9_]*$` y no se repiten en 1000 llamadas a la función interna de nombres.
6. `TestSweepRemovesOnlyStaleObjects`: crea un esquema con prefijo temporal antiguo y otro reciente; tras el barrido solo desaparece el antiguo.
7. Criterio 4: revisión de `ci.yml` (servicio `postgres:17`, `DATABASE_URL`, validación de rama) y ejecución del job `go` en la PR; sin prueba Go.
8. Criterios 5 y 6: `task docs:check` en verde y revisión: `grep -ri testcontainers docs/07-ingenieria docs/02-arquitectura` sin resultados.
9. Regresión: `kernel/db/migrate_test.go` (4 tests) y `kernel/architecture_test.go` en verde.

## Riesgos técnicos y mitigación
- **Primera migración real con `kernel.` fijo:** `Schema(t)` no sirve para migraciones que califican `kernel.`; por eso `Database` usa el esquema real. Quien escriba tablas debe usar `Database`.
- **`CREATE DATABASE` exige `CREATEDB`:** el usuario del compose y del servicio de CI es el dueño (superusuario). Si en el futuro cambia a un rol limitado (HU-kernel-spaces-03 introduce el rol de la API), el arnés debe conectar como el rol de migraciones; se deja `URL()` como único punto de configuración.
- **Límite de conexiones (100 por defecto):** `go test` corre paquetes en paralelo, cada prueba abre un pool. Tope de 4 por pool y 2 en el administrativo; si aparece `too many clients`, usar `-p`/`-parallel` en `task test:go` y documentarlo.
- **Costo de `CREATE DATABASE` (decenas de ms):** aceptable; si crece, evaluar `TEMPLATE` con las migraciones ya aplicadas (fuera de alcance ahora).
- **Huérfanos tras un corte:** barrido por antigüedad; el umbral de una hora evita borrar bases de un paquete concurrente.
- **Prueba de fallo con `t.FailNow`:** probar la limpieza tras un fallo sin hacer fallar la suite es delicado; si no es viable, queda cubierto por el uso exclusivo de `t.Cleanup` y se justifica en la PR.
- **Importación desde otras capas:** el paquete importa `kernel/db`; las dependencias son de `platform` hacia kernel, permitidas. La prueba de arquitectura debe seguir en verde.
- **Documentación de producto desfasada:** `docs/product/contexto-dominio.md` aún menciona Testcontainers como contexto histórico; no se edita (no es documentación de ingeniería).
- La historia se puede construir tal como está escrita; no está bloqueada.

## Fuera de alcance
Pruebas de carga, Redis, la batería completa de aislamiento de B6, matriz de versiones de PostgreSQL, plantillas de base (`TEMPLATE`), tablas del kernel y la prueba de aislamiento misma (la escriben las historias de tablas de M1; aquí solo se deja anotada en la hoja de ruta), roles de base de datos, cambios en `infra/docker/compose.yaml`.
