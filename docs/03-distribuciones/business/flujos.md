# Flujos de Thrive Business

## 1. Del registro al uso

```mermaid
flowchart LR
    A["Registro<br/>(Clerk)"] --> B["Elige rubro<br/>y responde"]
    B --> C["Business crea<br/>el negocio"]
    C --> D["IA configura y<br/>primeros pasos"]
    D --> E["Uso diario<br/>vende y cobra"]
    E --> F["Reportes y<br/>finanzas"]
```

1. **Registro.** La persona inicia sesión con Clerk.
2. **Rubro y preguntas.** Elige su rubro y responde unas pocas preguntas sobre cómo opera.
3. **Creación.** Business crea el negocio con la piel del rubro, en una sola operación: participante, dueño, capacidades y configuración.
4. **Configuración por IA.** La IA ajusta según las respuestas, solo dentro de lo permitido por la piel. Si algo necesita aprobación, va al equipo de NexusThrive.
5. **Uso diario.** Desde cualquier cara: web, móvil o escritorio.
6. **Reportes y finanzas.** Caja, cartera y resultados, también desde cualquier cara.

## 2. Cobrar una membresía, de punta a punta

```mermaid
sequenceDiagram
    participant App as App recepción
    participant K as Kernel
    participant DB as PostgreSQL
    participant N as n8n
    participant W as WhatsApp del socio

    App->>K: Asignar plan y cobrar (SDK)
    K->>K: Valida token, negocio, permiso y capacidad
    K->>DB: Una transacción: membresía + deuda + pago + recibo + señal
    K-->>App: Listo, con recibo
    DB-->>N: Señal: membresía pagada
    N->>W: Recibo y bienvenida
    Note over K,N: Días después
    K->>N: Señal: membresía vence en 3 días
    N->>W: Recordatorio con enlace de pago
```

Si n8n falla, el cobro ya quedó guardado: solo se atrasa el aviso. **La plata nunca depende de n8n.**

## 3. Una venta en tienda

```mermaid
flowchart LR
    A["POS, tienda en línea<br/>o cotización aprobada"] --> B["Una sola operación:<br/>registrar venta"]
    B --> C{"¿Pagada?"}
    C -->|"sí"| D["Intercambio: dinero entra,<br/>existencias salen, recibo"]
    C -->|"no"| E["Compromiso: deuda<br/>en cartera"]
    E -->|"al pagar"| D
    D --> F["Señal: venta"]
    F --> G["n8n: recibo al cliente"]
```

No importa por dónde entre la venta: siempre termina en la misma operación de la base.

## 4. El dueño necesita algo nuevo

```mermaid
flowchart LR
    A["Dueño: Solicitar función"] --> B{"¿Está permitida<br/>en su piel?"}
    B -->|"sí"| C["La IA la activa y avisa"]
    B -->|"no"| D["Cola del equipo<br/>con recomendación de la IA"]
    D --> E["El equipo aprueba o rechaza"]
```

El dueño nunca ve interruptores de módulos. Pide; el sistema decide con reglas.
