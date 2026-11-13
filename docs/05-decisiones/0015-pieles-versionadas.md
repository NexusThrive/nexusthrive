# ADR-0015 · Pieles versionadas con despliegue controlado

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

Cambiar una piel sin control puede alterar las capacidades de cientos de negocios a la vez.

## Decisión

Cada piel se publica como una versión inmutable desde la consola. Aplicarla a espacios existentes es un despliegue explícito, con simulación previa, que solo toca capacidades de origen `package`.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Aplicar cambios automáticamente | Cambios inesperados para los clientes. |
| Nunca actualizar negocios existentes | Clientes antiguos quedan rezagados. |

## Consecuencias

### Positivas

- Cambios previsibles y reversibles.
- Se sabe qué versión usa cada espacio.

### Negativas

- Un paso más para publicar mejoras.

### Riesgos

- Despliegues olvidados a medio camino.

## Cumplimiento

`platform.skin_rollout_items` registra el resultado por espacio; la consola muestra despliegues incompletos.

## Referencias

- [Flujos de la plataforma](../02-arquitectura/plataforma/flujos.md)
