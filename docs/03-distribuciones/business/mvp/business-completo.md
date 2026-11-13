# Business completo: cómo se toca todo

## Propósito

Mostrar la primera aplicación de NexusThrive (Thrive Business con la piel Gimnasio) recorriendo **todas** las capas: cara, contrato, kernel, distribución, base de datos, señales, integraciones e IA.

## Alcance

Etapa 2, sobre la base terminada en la etapa 1.

## Funcionamiento

### 1. Mapa de módulos y cómo se conectan

```mermaid
flowchart LR
    subgraph BASE["Base de Business"]
        CLI["Clientes"]
        CAT["Catálogo"]
        VEN["Ventas y cobros"]
        CAR["Cartera"]
        CAJ["Caja"]
        REC["Recibos"]
        REP["Reportes"]
        EQU["Equipo y roles"]
    end
    subgraph MEM["Patrón Membresía"]
        PLA["Planes"]
        MBR["Membresías"]
        ACC["Acceso y check-in"]
    end
    CAT --> VEN
    CLI --> VEN
    VEN -->|"pagada"| CAJ
    VEN -->|"a crédito"| CAR
    CAR -->|"pago recibido"| CAJ
    VEN --> REC
    PLA -->|"es un ítem del"| CAT
    MBR -->|"se cobra con"| VEN
    MBR --> ACC
    CLI --> MBR
    CAJ --> REP
    CAR --> REP
    ACC --> REP
    EQU -.->|"permisos"| VEN
    EQU -.->|"permisos"| CAJ
```

Una membresía **no tiene su propio cobro**: usa Ventas y cobros de la base. Por eso la caja, la cartera y los reportes ya la ven sin código extra.

### 2. Una operación atravesando todas las capas

Asignar un plan mensual a un socio que paga la mitad y queda debiendo el resto:

```mermaid
sequenceDiagram
    autonumber
    participant R as Recepción (web o móvil)
    participant SDK as SDK (contracts)
    participant GW as Kernel · gateway
    participant CAP as Kernel · capacidades
    participant BUS as Business · patrón Membresía
    participant TR as Business · traductor
    participant EXE as Kernel · ejecución
    participant DB as PostgreSQL
    participant CO as Kernel · coordinación
    participant N8N as n8n
    participant WA as WhatsApp

    R->>SDK: memberships.Assign(socio, plan, pago parcial)
    SDK->>GW: Connect + token de Clerk + Idempotency-Key
    GW->>CAP: ¿token válido? ¿espacio activo? ¿money.handle? ¿Membresía activa?
    CAP-->>GW: permitido
    GW->>BUS: ejecutar Assign
    BUS->>TR: membresía + venta + pago parcial + deuda
    TR->>EXE: 1 compromiso de acceso, 1 intercambio de dinero, 1 compromiso de deuda
    EXE->>DB: una transacción: entries suman cero, compromisos, eventos, outbox
    EXE->>CO: agendar timers: vence en 30 días, deuda vence en 5
    EXE-->>R: listo: membresía activa, recibo, saldo pendiente
    CO->>N8N: señal membership.assigned.v1
    N8N->>WA: recibo y bienvenida
    Note over CO,N8N: 5 días después
    CO->>N8N: señal debt.overdue.v1
    N8N->>WA: recordatorio con enlace de Wompi
```

### 3. Lo que ve cada actor en un día

```mermaid
flowchart TB
    subgraph REC["Recepción"]
        R1["Abre caja"] --> R2["Socios entran con QR"]
        R2 --> R3["Vende plan o producto"]
        R3 --> R4["Recibe pagos de deudas"]
        R4 --> R5["Cierra caja: contado vs sistema"]
    end
    subgraph DUE["Dueño"]
        D1["Lee el resumen del día<br/>(IA)"] --> D2["Revisa morosos y<br/>aprueba mensajes sugeridos"]
        D2 --> D3["Mira reportes del mes"]
        D3 --> D4["Ajusta planes y precios"]
    end
    subgraph SIS["Sistema, solo"]
        S1["Recordatorios de vencimiento"]
        S2["Rechazo de entradas vencidas"]
        S3["Fin de prueba y avisos"]
        S4["Resumen diario"]
    end
    R5 -.->|"alimenta"| D1
    S4 -.-> D1
```

### 4. Dónde vive cada pieza en el código

| Pieza | Carpeta |
|---|---|
| Pantallas de recepción y dueño | `packages/app` + `apps/web` + `apps/mobile` |
| Operaciones `business.v1` | `contracts/proto/business/v1/` |
| Puertas, permisos, transacciones, reloj | `kernel/` |
| Clientes, ventas, cartera, caja, recibos, reportes | `distributions/business/base/` |
| Planes, membresías, acceso | `distributions/business/patterns/membership/` |
| Vocabulario, roles y preguntas del gimnasio | `distributions/business/skins/gym.yaml` |
| Wompi | `distributions/business/adapters/wompi/` |
| Configuración y resumen por IA | `distributions/business/ai/` |
| Recibo, bienvenida, recordatorios | `integrations/n8n/workflows/` |
