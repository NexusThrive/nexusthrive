# ADR-0001 · El kernel no conoce ningún mundo

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

Si el núcleo sabe qué es un cliente o una factura, cada mundo nuevo obliga a modificarlo y termina lleno de casos especiales. NexusThrive quiere servir a negocios, comunidades y personas con el mismo núcleo.

## Decisión

El kernel solo conoce siete piezas (participante, recurso, intercambio, compromiso, evento, tiempo, regla) y cumple cinco leyes. Cada distribución trae un traductor que convierte su idioma en esas piezas.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Un núcleo de negocios con módulos por rubro | Repite el problema de un ERP: cada rubro nuevo toca el núcleo. |
| Un sistema distinto por mundo | Duplica identidad, permisos, dinero y eventos. |

## Consecuencias

### Positivas

- Un mundo nuevo no modifica `kernel/`.
- El kernel se puede probar sin ninguna distribución.

### Negativas

- Diseñar el traductor exige más cuidado al principio.

### Riesgos

- Que se cuele vocabulario de negocio en el kernel por comodidad.

## Cumplimiento

Una prueba de arquitectura en `kernel/` falla si aparecen identificadores de una distribución (`customer`, `invoice`, `sale`, `membership`…) o importaciones de `platform/` o `distributions/`.

## Referencias

- [Modelo universal](../01-modelo/primitivas.md)
- [Kernel](../02-arquitectura/kernel.md)
