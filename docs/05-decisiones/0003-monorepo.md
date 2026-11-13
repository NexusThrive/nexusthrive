# ADR-0003 · Un solo repositorio con estructura de monorepo

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

Casi todo cambio toca el contrato, el kernel y una cara a la vez. En varios repositorios esos cambios se desincronizan.

## Decisión

Un monorepo `nexusthrive` con `kernel/`, `platform/`, `distributions/`, `services/`, `apps/`, `packages/`, `contracts/`, `integrations/`, `infra/`, `tools/` y `docs/`, usando `go.work`, pnpm workspaces y Turborepo.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Un repositorio por pieza | Cada cambio de contrato exige coordinar varios pull requests. |
| Monorepo sin estructura | Las dependencias se cruzan sin control. |

## Consecuencias

### Positivas

- Un cambio de contrato se revisa completo en un solo pull request.
- El CI corre solo lo que cambió.

### Negativas

- El repositorio crece; exige herramientas de monorepo.

### Riesgos

- Que las carpetas se importen entre sí y se pierdan los límites.

## Cumplimiento

Reglas de dependencia en [estructura.md](../02-arquitectura/estructura.md), verificadas por la prueba de arquitectura y por la configuración de los workspaces.

## Referencias

- [Estructura del monorepo](../02-arquitectura/estructura.md)
