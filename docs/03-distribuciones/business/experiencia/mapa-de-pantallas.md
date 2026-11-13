# Mapa de pantallas de Business

## Propósito

Listar las pantallas de la app de un negocio y quién ve cada una.

## Alcance

Web y móvil de recepción en la etapa 2. La consola NexusThrive está en [plataforma/consola.md](../../../02-arquitectura/plataforma/consola.md).

## Funcionamiento

### 1. La app del negocio

```mermaid
flowchart TB
    APP["App del negocio"]
    APP --> INI["Inicio<br/>resumen del día · primeros pasos · alertas"]
    APP --> CLI["Socios / Clientes<br/>lista · ficha · nuevo · importar"]
    APP --> COB["Cobrar<br/>venta rápida · asignar plan · enlace de pago"]
    APP --> ACC["Entradas<br/>escanear QR · historial · rechazos"]
    APP --> MEM["Membresías<br/>planes · por vencer · congeladas"]
    APP --> CAR["Cartera<br/>quién debe · recibir pago · mensajes sugeridos"]
    APP --> CAJ["Caja<br/>abrir · gastos · cerrar · turnos"]
    APP --> REP["Reportes<br/>ventas · ingresos y gastos · asistencia"]
    APP --> EQU["Equipo<br/>miembros · invitar · roles"]
    APP --> AJU["Ajustes<br/>negocio · medios de pago · avisos<br/>solicitar función · suscripción"]
    APP --> IA["Asistente<br/>pregunta lo que quieras"]

    CLI --> FICHA["Ficha del socio<br/>datos · membresía · pagos · deudas<br/>entradas · carnet QR"]
```

La palabra "Socios" sale de la piel Gimnasio. En la piel Tienda la misma pantalla dice "Clientes".

### 2. Qué ve cada rol

| Pantalla | Dueño | Administrador | Recepción |
|---|---|---|---|
| Inicio | completo | completo | turno de hoy |
| Socios | sí | sí | sí |
| Cobrar | sí | sí | sí |
| Entradas | sí | sí | sí |
| Membresías (planes) | editar | editar | ver |
| Cartera | sí | sí | recibir pagos |
| Caja | todas | todas | solo la suya |
| Reportes | sí | sí | no |
| Equipo | sí | sí | no |
| Ajustes | sí | sin suscripción | no |
| Asistente | sí | sí | preguntas de su rol |

### 3. El móvil de recepción

```mermaid
flowchart LR
    M["Móvil recepción"] --> M1["Escanear<br/>(pantalla principal)"]
    M --> M2["Cobro rápido"]
    M --> M3["Buscar socio"]
    M --> M4["Mi caja"]
    M1 --> R{"¿Membresía activa?"}
    R -->|"sí"| OK["Verde: bienvenido<br/>(aviso si vence pronto)"]
    R -->|"no"| NO["Rojo: motivo<br/>+ botón Renovar y cobrar"]
```

Sin internet: escanea contra la copia local, guarda las entradas y los cobros, y sincroniza al volver la conexión. Cada operación lleva su clave de idempotencia para no duplicarse.

## Reglas e invariantes

- Los textos visibles salen de `@nexusthrive/i18n`; la palabra "Socios" la pone la piel.
- Diseño según [06-reglas/diseno-de-experiencia.md](../../../06-reglas/diseno-de-experiencia.md).
