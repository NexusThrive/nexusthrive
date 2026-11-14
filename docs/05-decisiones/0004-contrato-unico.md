# ADR-0004 · Un contrato único del que se genera todo

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

Sin una fuente única, la API, el SDK, las herramientas MCP y los eventos se desincronizan.

## Decisión

Las operaciones se definen en Protobuf con Connect-RPC y los eventos en JSON Schema, dentro de `contracts/`. De ahí se genera el código de Go, TypeScript y Python, y se exponen las herramientas MCP.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| OpenAPI escrito a mano | Más fácil de empezar, pero se aleja del código con el tiempo. |
| GraphQL | No encaja con operaciones con riesgo, permiso e idempotencia declarados. |

## Consecuencias

### Positivas

- Nada se escribe a mano en los extremos.
- Los cambios incompatibles se detectan en el CI.

### Negativas

- Cada operación nueva pasa primero por el contrato.

### Riesgos

- Saltarse el contrato con endpoints ad hoc.

## Cumplimiento

`buf lint` y `buf breaking` en el CI. Las apps no pueden llamar al backend sin el SDK generado.

## Referencias

- [Contrato](../02-arquitectura/contrato.md)

## Enmienda 2026-11-14 · Código `idempotency_conflict`

La lista de códigos de error tipados suma `idempotency_conflict`: la misma clave de idempotencia con una solicitud distinta (otra operación, otro actor u otro cuerpo) mientras el registro está vigente. No escribe nada y su mensaje no revela el registro. No es `already_done` ni `invalid_input`. La decisión completa está en el [ADR-0026](0026-idempotencia-por-registro.md) y el código se documenta en [contrato.md](../02-arquitectura/contrato.md).
