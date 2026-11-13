# Onboarding

## Propósito

Definir el recorrido de un dueño desde el primer clic hasta su primer cobro.

## Alcance

Registro, asistente de creación, preparación por IA, estados del negocio, primeros pasos e invitaciones. La suscripción la gestiona la [plataforma](../../../02-arquitectura/plataforma/flujos.md).

## Conceptos principales

**Meta:** que un dueño pase de "no tengo cuenta" a "cobré mi primer plan" en menos de 15 minutos, sin configurar módulos.

## Funcionamiento

### 1. El recorrido completo

```mermaid
flowchart TB
    L["Página pública<br/>nexusthrive.com"] --> A["Crear cuenta<br/>correo o Google (Clerk)"]
    A --> V["Verificar correo"]
    V --> Q{"¿Tiene invitación<br/>a un negocio?"}
    Q -->|"sí"| INV["Acepta y entra al negocio<br/>con su rol"]
    Q -->|"no"| E{"¿Ya tiene<br/>un negocio?"}
    E -->|"sí"| H["Inicio de su negocio"]
    E -->|"no"| W["Asistente: crear negocio"]
    W --> P["La IA prepara el espacio"]
    P --> F["Inicio con primeros pasos"]
    F --> U["Primer cobro"]
```

### 2. El asistente de creación

Seis pasos cortos. Cada paso guarda lo escrito: si la persona cierra la ventana, vuelve donde iba.

```mermaid
flowchart LR
    S1["1 · Tu negocio<br/>nombre · ciudad<br/>enlace propio"] --> S2["2 · ¿Qué haces?<br/>elige rubro<br/>(define la piel)"]
    S2 --> S3["3 · Cómo operas<br/>3 a 5 preguntas<br/>de la piel"]
    S3 --> S4["4 · Tu equipo<br/>invitar personas<br/>(opcional)"]
    S4 --> S5["5 · Tu prueba<br/>días gratis<br/>sin tarjeta"]
    S5 --> S6["6 · Resumen<br/>Crear negocio"]
```

#### Preguntas de la piel Gimnasio (paso 3)

| Pregunta | Para qué la usa la IA |
|---|---|
| ¿Cuántos socios activos tienes, más o menos? | Sugerir plan de NexusThrive y si conviene importar |
| ¿Cómo te pagan? (efectivo, transferencia, Nequi, tarjeta) | Configurar medios de pago y sugerir enlaces Wompi |
| ¿Controlas la entrada de los socios? | Activar check-in con QR y el móvil de recepción |
| ¿Les recuerdas cuándo vence su plan? | Activar recordatorios por WhatsApp |
| ¿Vendes productos (bebidas, suplementos)? | Sugerir solicitar la capacidad de inventario simple |

La IA **solo** activa lo permitido por la piel; lo demás queda como solicitud para el equipo NexusThrive.

### 3. Qué pasa por dentro al crear

```mermaid
sequenceDiagram
    autonumber
    participant W as Asistente (web)
    participant K as Kernel
    participant B as Business
    participant IA as IA de negocio
    participant DB as PostgreSQL
    participant N as n8n

    W->>K: onboarding.CreateBusiness(datos, piel, respuestas)
    K->>B: crear negocio
    B->>DB: una transacción: espacio, dueño, permisos, piel, semillas (impuestos, numeración, caja)
    B-->>W: negocio creado
    W->>W: pantalla "Preparando tu espacio"
    K->>IA: señal business.created.v1
    IA->>B: aplicar ajustes según respuestas (dentro de la piel)
    IA->>B: crear planes de ejemplo y primeros pasos
    K->>N: señal business.created.v1
    N->>N: correo de bienvenida al dueño
    B-->>W: espacio listo
```

Si la IA o n8n fallan, el negocio ya existe y funciona; los ajustes se reintentan solos y el estado se ve en la consola interna.

### 4. Estados del negocio

Un negocio tiene dos estados independientes: **dónde va en su arranque** y **cómo está su suscripción**.

#### Arranque

```mermaid
stateDiagram-v2
    [*] --> Creando
    Creando --> Preparando: transacción lista
    Preparando --> PrimerosPasos: IA terminó
    PrimerosPasos --> Operando: primer cobro registrado
    Operando --> Operando: uso diario
```

#### Suscripción

```mermaid
stateDiagram-v2
    [*] --> Prueba
    Prueba --> Activa: paga
    Prueba --> Vencida: termina sin pagar
    Vencida --> Activa: paga
    Activa --> Suspendida: falta de pago o decisión del equipo
    Suspendida --> Activa: se resuelve
```

En **Vencida** el dueño puede ver y exportar sus datos, pero no registrar operaciones nuevas.

### 5. Primeros pasos del gimnasio

Una lista en el Inicio que **se marca sola** leyendo los datos. Nada se marca a mano.

```mermaid
flowchart TB
    P1["Revisa tus planes<br/>(la IA creó ejemplos)"] --> P2["Agrega tus socios<br/>uno por uno o importando Excel"]
    P2 --> P3["Asigna un plan y cobra"]
    P3 --> P4["Entrega el carnet con QR"]
    P4 --> P5["Registra la primera entrada"]
    P5 --> P6["Cierra tu primera caja"]
    P6 --> OK["Listo: tu gimnasio opera en NexusThrive"]
```

### 6. Invitación de un miembro del equipo

```mermaid
flowchart LR
    D["Dueño: Equipo → Invitar<br/>correo + rol"] --> M["Correo con enlace"]
    M --> C["Crea cuenta o inicia sesión"]
    C --> E["Entra directo al negocio<br/>con su rol"]
    E --> T["Ve solo lo de su rol<br/>(recepción: check-in, cobros, caja)"]
```
