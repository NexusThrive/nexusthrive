# ADR-0022 · Cobro de NexusThrive con Wompi

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

NexusThrive debe cobrar la suscripción a los negocios. Hay que elegir pasarela, el recorrido de la prueba al pago y qué pasa cuando alguien no paga. Los precios y límites todavía no están validados con clientes.

## Decisión

**Wompi es solo la pasarela con la que NexusThrive cobra la suscripción a los negocios.** Las tiendas registran sus propios cobros como métodos manuales: efectivo, transferencia, datáfono propio y Nequi.

**Prueba.** El registro no cobra: 15 días de prueba con todo incluido y sin tarjeta.

**Pago.** Al vencer, el dueño elige un plan y paga con Wompi (checkout o enlace; tarjeta, PSE o Nequi). El webhook de Wompi, **verificado e idempotente**, activa o renueva el periodo. La consola permite confirmar un pago a mano como respaldo.

**Cobranza.** Recordatorios antes y después del vencimiento, días de gracia y luego **solo lectura**. Nunca se borra información. Pagar reactiva el espacio.

**Planes.** Essential, Elite y Signature. Precios en COP, ciclo mensual o anual (20 % de descuento anual) y límites son **datos editables desde la consola**. Los valores iniciales están marcados `provisional` y deben confirmarse antes de activar Wompi en producción.

**Pospuesto:** cobro recurrente automático con tarjeta guardada.

Estados de la suscripción: `prueba`, `activa`, `en mora`, `suspendida`.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Wompi también para las ventas de las tiendas | Convertiría a NexusThrive en intermediario de pagos de terceros; queda fuera del MVP. |
| Cobro manual únicamente | No escala y retrasa la activación. |
| Otra pasarela (Stripe, PayU, Mercado Pago) | Wompi cubre PSE y Nequi, medios habituales en Colombia. |
| Precios y límites en el código | Cambiarlos exigiría un despliegue. |
| Borrar datos al impago | Destruye confianza y puede incumplir obligaciones de conservación. |

## Consecuencias

### Positivas

- Activación inmediata tras el pago, sin intervención humana.
- Precios ajustables sin tocar el código.

### Negativas

- Cada renovación requiere que el dueño pague de nuevo hasta que exista el cobro recurrente.

### Riesgos

- Webhooks falsos o repetidos. Mitigación: verificación de firma e idempotencia por referencia de transacción.
- Precios provisionales publicados por error. Mitigación: confirmación humana antes de producción.

## Cumplimiento

- Pruebas: un webhook repetido no duplica el periodo; uno con firma inválida se rechaza; el reloj simulado lleva una prueba a mora y a solo lectura, y un pago la reactiva.
- La activación de Wompi en producción es un punto de control humano.

## Referencias

- [ADR-0005](0005-integraciones.md)
- [ADR-0020](0020-control-externo.md)
- [Plataforma](../02-arquitectura/plataforma/README.md)
