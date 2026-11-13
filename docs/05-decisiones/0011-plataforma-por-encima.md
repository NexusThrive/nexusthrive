# ADR-0011 · La plataforma está por encima de los espacios, con acceso auditado

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

NexusThrive necesita crear, cobrar, configurar y dar soporte a todos los espacios sin convertirse en un superusuario invisible.

## Decisión

La capa `platform/` es una extensión privilegiada del kernel con su esquema y su consola. El equipo vive en el espacio `platform`. Todo acceso a datos de un cliente es temporal, con motivo y auditado; la escritura requiere aprobación de otra persona.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Superusuario con acceso total | Sin trazabilidad; riesgo legal y de confianza. |
| Panel fuera del kernel con acceso directo a la base | Rompe las leyes y los permisos. |

## Consecuencias

### Positivas

- Operación de toda la plataforma desde un solo lugar.
- Confianza del cliente: ve quién accedió y por qué.

### Negativas

- Un flujo de soporte más lento que el acceso directo.

### Riesgos

- Roles del equipo mal asignados.

## Cumplimiento

`kernel.access_audit` para cada acceso privilegiado; revisión mensual de roles del equipo.

## Referencias

- [Plataforma](../02-arquitectura/plataforma/README.md)
- [Consola](../02-arquitectura/plataforma/consola.md)
