# ADR-0017 · MVP web: Tienda primero

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

La hoja de ruta de la etapa 2 empezaba por Membresía (gimnasio) con una app móvil de recepción, y dejaba la tienda para la fase N5. Para llegar a un MVP vendible hay que recortar: cada app y cada piel extra multiplica el trabajo de diseño, pruebas y soporte. Una tienda ejercita el libro de movimientos completo (ventas, existencias, compras, cartera, caja) y el patrón Productos es el que más valor demuestra sobre el kernel.

## Decisión

El MVP vendible es **Thrive Business para tiendas, solo en web**: patrón Productos con piel Tienda, servido por `apps/web`. Gimnasio (Membresía), Restaurante (Preparación), `apps/mobile` y `apps/desktop` empiezan después de la v1.0. Las fases N1 a N6 de la etapa 2 se reordenan: N1 (base de Business) y N2 (Tienda) pasan a ser el MVP; el resto se difiere.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Gimnasio primero, como estaba planeado | Exige además la app móvil de recepción y el flujo de QR; dobla las superficies antes de vender. |
| Gimnasio y Tienda a la vez | Dos pieles y dos públicos sin haber validado ninguno. |
| Tienda con móvil desde el inicio | La caja y el POS funcionan bien en web con lector USB; el móvil puede esperar. |

## Consecuencias

### Positivas

- Un solo cliente (web) y un solo público para validar el producto.
- La tienda prueba cartera, existencias, compras y caja sobre el mismo libro.

### Negativas

- Lo ya documentado para gimnasio queda en espera y puede desactualizarse.

### Riesgos

- Que el patrón Productos arrastre supuestos de tienda que luego estorben a Preparación. Mitigación: los nombres de tienda viven en la piel y en `business`, nunca en el kernel (ADR-0001).

## Cumplimiento

- La hoja de ruta y el [MVP de Business](../03-distribuciones/business/mvp/README.md) reflejan el nuevo orden.
- No se crean paquetes ni pantallas de móvil, escritorio, gimnasio o restaurante antes de la v1.0.

## Referencias

- [ADR-0008](0008-patrones-y-pieles.md)
- [Hoja de ruta](../02-arquitectura/hoja-de-ruta.md)
