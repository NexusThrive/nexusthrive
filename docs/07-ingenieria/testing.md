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
| Integración | Operaciones completas contra PostgreSQL real | `go test` con Testcontainers |
| Aislamiento | Ningún dato cruza entre espacios | Pruebas dedicadas por esquema |
| Idempotencia | Repetir una operación de dinero no la duplica | Pruebas dedicadas por operación |
| Arquitectura | Dependencias prohibidas y vocabulario prohibido en `kernel/` | Prueba de Go que analiza importaciones e identificadores |
| Contrato | Compatibilidad hacia atrás | `buf breaking` |
| Componentes | Los cuatro estados de cada componente | Storybook + Vitest |
| Punta a punta | Recorridos principales en la web y la consola | Playwright |

## Reglas e invariantes

1. Toda operación nace con su prueba, escrita antes que la implementación.
2. Las pruebas de `kernel/laws/` nunca se desactivan ni se marcan para omitir.
3. Nombres de prueba en inglés y descriptivos: `TestExchangeRejectsUnbalancedEntries`.
4. Una prueba que falla de forma intermitente se arregla o se elimina; nunca se reintenta hasta que pase.
5. Datos de prueba creados por la propia prueba; nada depende del orden de ejecución.
