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
    subgraph E2["Etapa 2 · Thrive Business"]
        direction TB
        N1["N1 · Base de Business<br/>+ web + onboarding"] --> N2["N2 · Membresía / Gimnasio<br/>+ móvil de recepción"]
        N2 --> N3["N3 · Conectado<br/>Wompi, n8n, IA"]
        N3 --> N4["N4 · Piloto"]
        N4 --> N5["N5 · Productos / Tienda"]
        N5 --> N6["N6 · Preparación / Restaurante"]
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
| N1 a N6 | Ver [MVP de Business](../03-distribuciones/business/mvp/README.md) |

## Reglas e invariantes

- No empieza la etapa 2 hasta terminar B5. B6 puede avanzar en paralelo con N1.
- Una fase no está terminada mientras su criterio no se cumpla con pruebas automáticas.
