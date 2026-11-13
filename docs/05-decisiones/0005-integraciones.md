# ADR-0005 · Lo crítico va directo; lo demás, por n8n

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

Pagos y facturación necesitan verificación, una sola transacción y cero duplicados. Avisos y conexiones cambian seguido y cada cliente los quiere distintos.

## Decisión

Pasarelas de pago y facturación electrónica son adaptadores directos de la distribución. Avisos y conexiones con otras apps los hace n8n escuchando señales y pidiendo acciones por la API. n8n nunca guarda ni mueve valor.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Todo por n8n | Un fallo de n8n dejaría pagos en el aire. |
| Todo dentro del kernel | Ensucia el núcleo con cada integración. |

## Consecuencias

### Positivas

- Si n8n falla, solo se atrasan avisos.
- Integraciones nuevas sin tocar el kernel.

### Negativas

- Dos caminos de integración que documentar.

### Riesgos

- Uso de n8n con credenciales de cada cliente sin la licencia comercial (n8n Embed). Pendiente de confirmar.

## Cumplimiento

Revisión de pull requests: ninguna operación de dinero depende de n8n.

## Referencias

- [Integraciones](../04-integraciones/README.md)
