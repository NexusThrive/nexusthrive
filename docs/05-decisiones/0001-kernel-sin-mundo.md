# 0001 · El kernel no conoce ningún mundo

**Estado:** aceptada

## Contexto
Si el núcleo sabe qué es un cliente o una factura, cada mundo nuevo obliga a modificarlo y termina lleno de casos especiales.

## Decisión
El kernel solo conoce siete piezas (participante, recurso, intercambio, compromiso, evento, tiempo, regla) y cinco leyes. Cada distribución trae su traductor.

## Consecuencias
- Un mundo nuevo no toca `kernel/`.
- Una prueba de arquitectura falla si aparece vocabulario de negocio dentro de `kernel/`.
- Diseñar el traductor exige más cuidado al principio.
