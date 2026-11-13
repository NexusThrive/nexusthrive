# MVP de Thrive Business

## Propósito

Definir qué se construye de Business en la etapa 2 y cuándo está terminado.

## Alcance

**Requisito previo:** la etapa 1 (base de NexusThrive) terminada hasta la fase B5. Ver [hoja de ruta](../../../02-arquitectura/hoja-de-ruta.md).

**Objetivo:** que una tienda pequeña opere todo su día en NexusThrive desde la web (ventas, existencias, compras, pedidos, cartera, caja y reportes) y pague por ello. Gimnasio y restaurante vienen después de la v1.0 ([ADR-0017](../../../05-decisiones/0017-mvp-web-tienda-primero.md)).

## Conceptos principales

```mermaid
flowchart LR
    subgraph SI["Entra en el MVP (v1.0)"]
        direction TB
        S1["Base de Business<br/>clientes · ventas y cobros · cartera<br/>caja · documentos · reportes · equipo"]
        S3["Patrón Productos<br/>piel Tienda: catálogo, existencias,<br/>POS, pedidos, compras, CRM"]
        S4["Solo web (apps/web)"]
        S5["Control externo: API, MCP,<br/>webhooks y n8n"]
        S6["AURI: onboarding y consultas"]
    end
    subgraph NO["Después de la v1.0"]
        direction TB
        N1["Gimnasio (Membresía)<br/>Restaurante (Preparación)"]
        N2["Móvil y escritorio"]
        N3["Varias bodegas o sedes"]
        N4["Tienda en línea"]
        N5["Factura electrónica DIAN"]
        N6["Contabilidad completa"]
        N7["Pasarelas de pago para los clientes de la tienda"]
        N8["Cobro recurrente con tarjeta guardada"]
    end
```

## Funcionamiento

| Fase | Entrega | Terminada cuando |
|---|---|---|
| N1 | Base de Business, `apps/web` y onboarding | Un dueño se registra, crea su tienda, invita a un cajero, abre caja, vende de contado y fiado, recibe un abono y cierra caja cuadrada; un webhook de esa venta llega a n8n |
| N2 | Tienda (patrón Productos, piel Tienda) | Una tienda vende desde el POS con lector; las existencias cuadran tras compras, ventas, devoluciones, pedidos y un conteo; la cartera coincide con el libro; cada operación se ejecuta y observa por API, MCP y n8n |
| Después de la v1.0 | Membresía con piel Gimnasio, Preparación con piel Restaurante, móvil y escritorio | Se definen en su momento con un ADR |

### Roles del negocio

| Rol | Qué hace |
|---|---|
| Dueño | Todo en su negocio: equipo, planes, precios, reportes, solicitar funciones |
| Administrador | Igual que el dueño, sin cerrar el negocio ni cambiar la suscripción |
| Cajero | Clientes, ventas, cobros y caja de su turno; no ve reportes de ganancias |

## Referencias

- [operaciones.md](operaciones.md): operaciones y señales.
- [business-completo.md](business-completo.md): una operación atravesando todas las capas.
- [Modelo de datos `business`](../../../02-arquitectura/modelo-de-datos/business.md).
- [Experiencia](../experiencia/README.md).
