# Hoja de ruta

## Propósito

Fijar el orden de construcción: primero la base de NexusThrive, después Thrive Business.

## Alcance

Las dos etapas y sus fases, con el criterio que dice cuándo cada una está terminada.

## Funcionamiento

```mermaid
flowchart LR
    subgraph E1["Etapa 1 · Base de NexusThrive"]
        direction TB
        B1["B1 · Andamiaje<br/>monorepo, CI, local"] --> B2["B2 · Núcleo del kernel<br/>espacios, identidad, permisos<br/>libro, compromisos, eventos"]
        B2 --> B3["B3 · Puertas<br/>API, MCP de lectura,<br/>webhooks, Clerk"]
        B3 --> B4["B4 · Plataforma<br/>equipo, planes, suscripciones,<br/>pieles, solicitudes, soporte"]
        B4 --> B5["B5 · Consola NexusThrive"]
        B5 --> B6["B6 · Endurecimiento<br/>seguridad, observabilidad,<br/>copias y restauración"]
    end
    subgraph E2["Etapa 2 · Thrive Business (MVP: tienda web)"]
        direction TB
        N1["N1 · Base de Business<br/>+ web + onboarding"] --> N2["N2 · Productos / Tienda<br/>POS, existencias, pedidos,<br/>compras, cartera"]
        N2 --> N3["Después de la v1.0<br/>Gimnasio, Restaurante,<br/>móvil y escritorio"]
    end
    E1 --> E2
```

| Fase | Terminada cuando |
|---|---|
| B1 | `task setup`, `task test` y `task lint` funcionan en una máquina limpia; el CI corre solo lo que cambió |
| B2 | Las cinco leyes tienen pruebas en verde; un intercambio y un compromiso de prueba se escriben en una transacción con su evento y su outbox |
| B3 | Una operación entra por la API con token de Clerk, se ejecuta, se repite con la misma clave sin duplicarse y su señal llega a un webhook firmado |
| B4 | La plataforma crea un espacio en prueba con su suscripción, publica una piel de prueba y procesa una solicitud de capacidad de punta a punta |
| B5 | El equipo opera desde la consola: crea negocios, confirma cobros, decide solicitudes, abre accesos de soporte auditados y reintenta tareas fallidas |
| B6 | Pruebas de aislamiento entre espacios, doble factor obligatorio para el equipo, métricas y alertas, y una restauración de copia probada |
| N1 y N2 | Ver [MVP de Business](../03-distribuciones/business/mvp/README.md) |

### Ruta al MVP

El MVP vendible es Thrive Business para tiendas, solo en web ([ADR-0017](../05-decisiones/0017-mvp-web-tienda-primero.md)). Cada fase B se entrega en su versión MVP y lo pospuesto está listado en el [ADR-0018](../05-decisiones/0018-recorte-de-la-etapa-1.md).

| Hito | Fase | Entrega |
|---|---|---|
| M1 | B2 | Núcleo del kernel |
| M2 | B3 | Puertas completas y staging en línea |
| M3 | — | Sistema de diseño mejorado y armazón |
| M4 | B4 | Plataforma MVP y cobro con Wompi |
| M5 | B5 | Consola MVP |
| M6 | N1 | Base de Business, `apps/web` y onboarding |
| M7 | N2 | Tienda (patrón Productos, piel Tienda) |
| M8 | B6 | Endurecimiento y lanzamiento v1.0 |

Después de la v1.0: Gimnasio, Restaurante, móvil, escritorio, varias sedes, tienda en línea, factura electrónica y cobro recurrente automático.

## Reglas e invariantes

- No empieza la etapa 2 hasta terminar B5. B6 puede avanzar en paralelo con N1 y se cierra antes de la v1.0.
- Una fase no está terminada mientras su criterio no se cumpla con pruebas automáticas.
