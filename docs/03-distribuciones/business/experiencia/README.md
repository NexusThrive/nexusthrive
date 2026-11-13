# Experiencia de Business

## Propósito

Reunir lo necesario para diseñar la app de los negocios.

## Alcance

| Página | Responde |
|---|---|
| [onboarding.md](onboarding.md) | Del primer clic hasta el primer cobro |
| [mapa-de-pantallas.md](mapa-de-pantallas.md) | Qué pantallas existen y quién ve cada una |

Los principios de diseño, comunes a todas las caras y a la consola, están en [06-reglas/diseno-de-experiencia.md](../../../06-reglas/diseno-de-experiencia.md).

## Conceptos principales

- **Web** (`apps/web`): dueño, administrador y recepción en computador.
- **Móvil de recepción** (`apps/mobile`): check-in con QR, cobro rápido y caja del turno. Funciona sin internet y sincroniza.
- Todas comparten `@nexusthrive/ui`, `@nexusthrive/app` y `@nexusthrive/i18n`. Cambia el envoltorio, no la experiencia.
