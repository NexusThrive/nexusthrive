# Pruebas

## Propósito

Definir qué se prueba en NexusThrive y cómo.

## Alcance

Kernel, plataforma, distribuciones, contratos y apps.

## Funcionamiento

| Nivel | Qué verifica | Herramientas |
|---|---|---|
| Leyes del kernel | Conservación, memoria, compromiso, permiso y movimiento | `go test ./kernel/laws/...` |
| Unitarias | Una función o un tipo, sin base de datos | `go test`, Vitest, `pytest` |
| Integración | Operaciones completas contra PostgreSQL real | `go test` contra el PostgreSQL de `task infra:up` en local y el servicio `postgres:17` en CI; arnés `kernel/db/dbtest` |
| Aislamiento | Ningún dato cruza entre espacios | Pruebas dedicadas por esquema |
| Idempotencia | Repetir una operación de dinero no la duplica | Pruebas dedicadas por operación |
| Arquitectura | Dependencias prohibidas y vocabulario prohibido en `kernel/` | Prueba de Go que analiza importaciones e identificadores |
| Contrato | Compatibilidad hacia atrás | `buf breaking` |
| Componentes | Los cuatro estados de cada componente | Storybook + Vitest |
| Punta a punta | Recorridos principales en la web y la consola | Playwright |

### Arnés de integración con PostgreSQL

El paquete `kernel/db/dbtest` entrega a cada prueba una base o un esquema propio:

- `dbtest.Database(t)` crea la base `kernel_test_<id>`, aplica las migraciones del kernel (esquema `kernel`) y la elimina al terminar. Es la opción para pruebas de tablas del kernel.
- `dbtest.Schema(t)` crea un esquema vacío `kernel_test_<id>` en la base compartida y lo elimina al terminar; no aplica migraciones.
- Cada prueba tiene su propia base o esquema, por lo que dos pruebas con `t.Parallel()` no se ven. La limpieza usa solo `t.Cleanup`, que corre también tras un fallo.
- La URL sale de `DATABASE_URL`; sin ella se usa la de `task infra:up` (puerto 55432). `platform/` y las distribuciones pueden importar el mismo paquete.
- Los restos de ejecuciones interrumpidas con más de una hora se eliminan en la primera llamada de cada proceso.
- Una prueba de integración sin PostgreSQL falla con la indicación de ejecutar `task infra:up`; nunca se omite.

## Reglas e invariantes

1. Toda operación nace con su prueba, escrita antes que la implementación.
2. Las pruebas de `kernel/laws/` nunca se desactivan ni se marcan para omitir.
3. Nombres de prueba en inglés y descriptivos: `TestExchangeRejectsUnbalancedEntries`.
4. Una prueba que falla de forma intermitente se arregla o se elimina; nunca se reintenta hasta que pase.
5. Datos de prueba creados por la propia prueba; nada depende del orden de ejecución.
