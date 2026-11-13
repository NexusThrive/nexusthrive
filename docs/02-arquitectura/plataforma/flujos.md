# Flujos de la plataforma

## Propósito

Describir los procesos que la plataforma ejecuta sobre los espacios: suscripción y cobranza, pieles, solicitudes, soporte y operación técnica.

## Alcance

Incluye los flujos que inicia el equipo NexusThrive o el reloj del kernel. Los flujos de uso diario de un negocio están en su distribución.

## Funcionamiento

### 1. Ciclo de una suscripción

```mermaid
stateDiagram-v2
    [*] --> Trialing: espacio creado
    Trialing --> Active: paga antes de terminar la prueba
    Trialing --> PastDue: termina la prueba sin pago
    Active --> PastDue: cobro vence sin pago
    PastDue --> Active: pago confirmado
    PastDue --> Suspended: pasan los días de gracia
    Suspended --> Active: pago confirmado
    Active --> Canceled: el cliente cancela
    Suspended --> Canceled: decisión del equipo
    Canceled --> [*]
```

| Estado | Qué puede hacer el negocio |
|---|---|
| `trialing`, `active` | Todo lo que permite su plan |
| `past_due` | Todo, con aviso permanente de pago pendiente |
| `suspended` | Ver y exportar sus datos; no registra operaciones nuevas |
| `canceled` | Exportar durante 30 días; después se archiva |

### 2. Cobranza mensual

```mermaid
flowchart LR
    A["Reloj: inicia el periodo"] --> B["platform crea el cobro"]
    B --> C["Señal charge.created"]
    C --> D["n8n: aviso al dueño<br/>con medios de pago"]
    D --> E{"¿Pagó antes<br/>del vencimiento?"}
    E -->|"sí"| F["Finanzas confirma<br/>con evidencia"]
    E -->|"no"| G["Recordatorios según<br/>la política"]
    G --> H{"¿Pagó en los<br/>días de gracia?"}
    H -->|"sí"| F
    H -->|"no"| I["Suspensión automática<br/>con aviso previo"]
    F --> J["Suscripción activa<br/>y recibo al dueño"]
```

### 3. Crear un negocio desde la consola

Para ventas asistidas: el equipo crea el negocio y el dueño solo lo recibe.

```mermaid
sequenceDiagram
    autonumber
    participant V as Ventas (consola)
    participant P as platform
    participant K as Kernel
    participant D as Distribución
    participant N as n8n

    V->>P: spaces.CreateTrial(prospecto, piel, días de prueba)
    P->>K: crear espacio + suscripción en prueba
    K->>D: aplicar la piel (source = package)
    P->>K: invitación al dueño con rol owner
    K->>N: señal invitation.created
    N->>N: correo y WhatsApp con el enlace
    Note over V,N: El dueño acepta, entra y completa sus primeros pasos
```

### 4. Publicar y desplegar una piel

```mermaid
flowchart LR
    A["Ingeniería crea<br/>borrador v3"] --> B["Operaciones revisa<br/>las diferencias con v2"]
    B --> C["Publicar v3<br/>(queda inmutable)"]
    C --> D["Simular despliegue:<br/>qué cambia en cada espacio"]
    D --> E{"¿Aprobado?"}
    E -->|"sí"| F["Desplegar a espacios<br/>seleccionados o a todos"]
    E -->|"no"| A
    F --> G["Cada espacio: solo cambian<br/>capacidades de origen package"]
    G --> H["Señal skin.applied<br/>por espacio"]
```

### 5. Solicitud de una capacidad

```mermaid
sequenceDiagram
    autonumber
    participant O as Dueño
    participant K as Kernel
    participant IA as IA de la plataforma
    participant C as Consola
    participant E as Equipo NexusThrive

    O->>K: capabilities.Request(capacidad, motivo)
    K->>IA: señal capability.requested
    IA->>IA: evalúa piel, plan, uso y dependencias
    alt está en las sugeridas de su piel
        IA->>K: activa con source = ai y motivo
        K-->>O: aviso: función activada
    else requiere decisión humana
        IA->>C: recomendación con razones
        E->>C: aprueba o rechaza
        C->>K: activa con source = admin, o rechaza con nota
        K-->>O: aviso con la decisión
    end
```

### 6. Acceso de soporte auditado

```mermaid
sequenceDiagram
    autonumber
    participant S as Soporte
    participant C as Consola
    participant P as platform
    participant K as Kernel
    participant A as Aprobador

    S->>C: abrir acceso (espacio, ticket, motivo, alcance)
    alt alcance de lectura
        C->>P: supportAccess.Open
    else alcance de escritura
        C->>A: pide aprobación
        A->>P: supportAccess.Approve
    end
    P->>K: grant temporal en el espacio (expira en 60 min)
    K-->>S: entra al espacio con aviso visible
    S->>K: cada lectura o escritura
    K->>K: registra en access_audit
    Note over S,K: Al expirar o al cerrar, el grant se revoca solo
    K->>K: señal support_access.ended
```

El dueño del negocio ve en su historial que hubo un acceso de soporte, quién, cuándo y por qué.

### 7. Suspender y reactivar

| Efecto de `spaces.Suspend` | Detalle |
|---|---|
| Caras | Mensaje de cuenta suspendida; ver y exportar siguen disponibles |
| Operaciones | Toda escritura responde `space_suspended` |
| Páginas y enlaces públicos | Muestran "no disponible"; los enlaces de pago no aceptan cobros nuevos |
| Webhooks entrantes | Se guardan sin procesar |
| Outbox y temporizadores | Se pausan para ese espacio |
| API y MCP | Responden `space_suspended` en escrituras |

Al reactivar, lo guardado se procesa en orden y los temporizadores vencidos se disparan una sola vez.

### 8. Tareas fallidas

```mermaid
flowchart LR
    A["Outbox, webhook o aviso<br/>falla"] --> B["Reintentos con espera creciente<br/>(hasta 8)"]
    B --> C{"¿Se entregó?"}
    C -->|"sí"| D["Listo"]
    C -->|"no"| E["Estado dead:<br/>aparece en Operación técnica"]
    E --> F["Ingeniería reintenta<br/>o descarta con motivo"]
```

## Decisiones relacionadas

- [ADR-0011 · La plataforma por encima de los espacios](../../05-decisiones/0011-plataforma-por-encima.md)
- [ADR-0015 · Pieles versionadas con despliegue controlado](../../05-decisiones/0015-pieles-versionadas.md)
