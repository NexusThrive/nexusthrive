# ADR-0010 · Un esquema de base de datos por capa

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

Si todas las capas escriben en las mismas tablas, los límites del código no existen en los datos.

## Decisión

PostgreSQL con los esquemas `kernel`, `platform` y uno por distribución (`business`). Cada capa solo escribe en el suyo y referencia al kernel.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Una base por capa | Rompe la transacción única por operación. |
| Un solo esquema | Mezcla responsabilidades. |

## Consecuencias

### Positivas

- Límites visibles en los datos.
- Una operación sigue siendo una sola transacción.

### Negativas

- Migraciones por esquema que coordinar.

### Riesgos

- Consultas que crucen esquemas sin pasar por el kernel.

## Cumplimiento

Cada capa se conecta con un rol de base de datos que solo puede escribir en su esquema.

## Referencias

- [Modelo de datos](../02-arquitectura/modelo-de-datos/README.md)
