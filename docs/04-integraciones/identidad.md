# Identidad

## Propósito

Definir cómo se autentica cada actor y quién decide sus permisos.

## Alcance

Personas, equipo NexusThrive, agentes de IA e integraciones.

## Funcionamiento

| Actor | Se autentica con | Sus permisos viven en |
|---|---|---|
| Persona de un negocio | Clerk (correo o Google) | `kernel.grants` del espacio |
| Equipo NexusThrive | Clerk con doble factor obligatorio | `platform.staff_members` y su rol |
| Agente de IA o integración | Llave (`kernel.api_keys`) | Alcances de la llave + `grants` de su participante |

## Reglas e invariantes

- Clerk solo prueba quién eres y entrega un token; el kernel decide qué puedes hacer.
- Cambiar de proveedor de identidad no toca nada más: solo cambia quién emite el token.

## Decisiones relacionadas

- [ADR-0006 · Clerk autentica, el kernel autoriza](../05-decisiones/0006-identidad.md)
