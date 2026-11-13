# ADR-0012 · Política de idiomas

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

Mezclar español e inglés sin criterio produce código y documentación inconsistentes, sobre todo con varias personas y agentes trabajando a la vez.

## Decisión

Documentación en español. Código, comentarios, errores internos, logs, pruebas, contratos, commits, pull requests, issues y ramas en inglés. Interfaz internacionalizada. Los nombres de producto no se traducen.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Todo en inglés | El equipo y los clientes leen mejor la documentación en español. |
| Todo en español | Identificadores y colaboración técnica menos claros. |

## Consecuencias

### Positivas

- Un solo estándar para personas y agentes.

### Negativas

- Exige disciplina al escribir.

### Riesgos

- Textos de usuario escritos directamente en el código.

## Cumplimiento

Revisión de pull requests con la lista de la plantilla y reglas de ESLint que prohíben textos literales en componentes.

## Referencias

- [Política de idiomas](../07-ingenieria/language-policy.md)
