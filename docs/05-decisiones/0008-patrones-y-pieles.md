# ADR-0008 · Patrones con código, pieles con configuración

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

Muchos rubros solo difieren en vocabulario y configuración. Escribir código por rubro repite el desorden.

## Decisión

Dentro de una distribución, un patrón es una forma de operar con funciones propias; una piel es un rubro sobre un patrón, descrito en YAML y publicado como versión desde la consola.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Un módulo por rubro | Código duplicado y rubros a medias. |
| Todo configurable | El motor de configuración se vuelve un lenguaje propio. |

## Consecuencias

### Positivas

- Un rubro nuevo casi siempre es solo configuración.

### Negativas

- Hay que diseñar bien cada patrón.

### Riesgos

- Forzar un rubro en un patrón que no le corresponde.

## Cumplimiento

Un patrón nuevo exige un ADR. Una piel nueva exige una versión publicada en `platform.skin_releases`.

## Referencias

- [Patrones](../03-distribuciones/business/patrones.md)
