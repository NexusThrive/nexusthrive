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
