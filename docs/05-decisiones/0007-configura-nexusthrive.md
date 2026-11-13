# ADR-0007 · El dueño no activa funciones

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

Dejar que cada dueño encienda y apague módulos crea combinaciones inválidas y un sistema difícil de sostener.

## Decisión

Las capacidades de un espacio las configuran el alta (según la piel), la IA (solo dentro de las sugeridas por la piel) y el equipo NexusThrive desde la consola. El dueño solicita con `capabilities.Request`.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Interruptores para el dueño | Combinaciones inválidas y soporte imposible. |
| Solo el equipo NexusThrive | No escala sin la ayuda de la IA. |

## Consecuencias

### Positivas

- Cada negocio recibe una configuración coherente.
- El origen de cada capacidad queda registrado.

### Negativas

- El dueño depende de una respuesta para funciones nuevas.

### Riesgos

- Que la cola de solicitudes crezca sin atención.

## Cumplimiento

`space_capabilities.source` solo admite `package`, `ai`, `admin` y `code`. No existe operación de activación para roles de un negocio.

## Referencias

- [Consola](../02-arquitectura/plataforma/consola.md)
