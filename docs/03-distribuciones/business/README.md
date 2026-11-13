# Thrive Business

**El mundo de todo lo que vende y cobra.** Es la primera distribución de NexusThrive.

```mermaid
flowchart TB
    subgraph PAT["Patrones (código propio) y pieles (configuración)"]
        M["Membresía<br/>gimnasio · academia"]
        PR["Productos<br/>tienda · merch · ferretería"]
        PE["Preparación<br/>restaurante · café"]
        SA["Servicio con agenda<br/>belleza · salud · taller · consultoría"]
        PY["Proyecto<br/>construcción"]
    end
    subgraph CAP["Capacidades que se suman"]
        C1["Inventario"] --- C2["Compras"] --- C3["POS"] --- C4["Tienda en línea"]
        C5["Agenda"] --- C6["Cotizaciones"] --- C7["Contabilidad"] --- C8["Varias sedes"]
    end
    subgraph BASE["Base: todos la tienen"]
        B1["Clientes"] --- B2["Catálogo"] --- B3["Ventas y cobros"] --- B4["Cartera"]
        B5["Caja"] --- B6["Facturas"] --- B7["Reportes"] --- B8["Equipo y roles"]
    end
    IA["IA de negocio"]
    TR["Traductor"]
    K["Kernel"]
    PAT --> CAP --> BASE --> TR --> K
    IA -.-> BASE
```

## La base

Todo negocio la tiene, sin importar el rubro:

- **Clientes:** fichas, historial, contacto.
- **Catálogo:** productos, servicios, planes, entradas.
- **Ventas y cobros:** una sola forma de registrar una venta, venga de donde venga.
- **Cartera:** lo que me deben y lo que debo, con vencimientos.
- **Caja y finanzas:** ingresos, gastos, caja menor, turnos.
- **Facturas:** recibos, facturas y facturación electrónica.
- **Reportes:** cómo va el negocio.
- **Equipo y roles:** quién trabaja y qué puede hacer.

## La IA de negocio

Cobra a los morosos, resume el día, alerta stock bajo, predice ventas y configura el negocio desde las respuestas del registro. Actúa con permisos propios y deja rastro.

## Capacidades

Se suman cuando el negocio las necesita: inventario y bodegas, compras y proveedores, punto de venta, tienda en línea y pedidos, agenda y citas, cotizaciones, contabilidad completa y varias sedes.

**El dueño no las activa.** NexusThrive y su IA configuran; el dueño solicita lo que necesite.

## El traductor

| Idioma Business | Pieza del kernel |
|---|---|
| Cliente, proveedor, empleado | Participante |
| Producto, servicio, plan, entrada | Recurso |
| Venta, pago, gasto, devolución | Intercambio |
| Deuda, membresía, cita, pedido por entregar | Compromiso |
| Vencimiento, turno, cierre de caja | Tiempo |
| Rol, permiso, política de precios | Regla |

Detalle de cada patrón en [patrones/](patrones/README.md) y los flujos en [flujos.md](flujos.md).
