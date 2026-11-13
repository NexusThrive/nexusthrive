# Avisos

## Propósito

Definir cómo NexusThrive avisa a las personas por cualquier canal.

## Alcance

WhatsApp, correo, SMS, push y Telegram. El contenido de cada aviso lo define la distribución.

## Funcionamiento

```mermaid
flowchart LR
    E["Señal del kernel"] --> D{"¿El destinatario<br/>dio consentimiento<br/>para ese canal?"}
    D -->|"no"| S["Aviso omitido<br/>con motivo"]
    D -->|"sí"| C{"¿Canal<br/>preferido?"}
    C --> W["WhatsApp"]
    C --> M["Correo"]
    C --> P["Push"]
    C --> T["SMS o Telegram"]
    W --> R["Registro en kernel.notices"]
    M --> R
    P --> R
    T --> R
```

## Reglas e invariantes

1. Sin consentimiento registrado en `contact_points` no se envía nada; si la persona se dio de baja, tampoco.
2. Cada aviso queda en `kernel.notices` con su estado: enviado, fallido u omitido, y el motivo.
3. La misma señal puede producir avisos en varios canales; el kernel no conoce los canales, solo la señal.
