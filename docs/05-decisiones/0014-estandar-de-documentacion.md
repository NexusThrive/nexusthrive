# ADR-0014 · Estándar de documentación y ADR

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

Documentación sin estructura se vuelve un ensayo difícil de consultar y se desactualiza.

## Decisión

Toda página sigue la estructura de [documentation-standard.md](../07-ingenieria/documentation-standard.md). Las decisiones importantes se escriben como ADR con la [plantilla](plantilla.md), antes del código, y nunca se editan una vez aceptadas.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Wiki libre | Sin estructura ni revisión. |
| Documentación solo en el código | No explica el porqué. |

## Consecuencias

### Positivas

- Páginas predecibles y fáciles de recorrer.
- Historial de decisiones con sus razones.

### Negativas

- Escribir antes de construir toma tiempo.

### Riesgos

- Documentos que no se actualizan con el código.

## Cumplimiento

La plantilla de pull request exige actualizar `docs/`; `task docs:check` valida los diagramas.

## Referencias

- [Estándar de documentación](../07-ingenieria/documentation-standard.md)
