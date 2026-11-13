# Seguridad de la plataforma

## Propósito

Definir cómo se protege el acceso privilegiado del equipo NexusThrive a los espacios de sus clientes.

## Alcance

Incluye autenticación del equipo, privilegios, accesos de soporte y auditoría. La seguridad general del sistema está en [`07-ingenieria/security.md`](../../07-ingenieria/security.md).

## Reglas e invariantes

1. **Doble factor obligatorio** para todo miembro del equipo NexusThrive.
2. **Mínimo privilegio:** cada rol tiene solo los permisos de su tabla en [consola.md](consola.md).
3. **Sin acceso permanente a datos de clientes.** Ver datos operativos de un negocio exige un acceso de soporte con motivo, ticket y vencimiento.
4. **Escritura con cuatro ojos:** un acceso de escritura necesita la aprobación de otra persona con `support.access.approve`.
5. **Todo queda escrito:** cada acción de la consola es un evento; cada lectura o escritura en un espacio ajeno queda en `kernel.access_audit`.
6. **Transparencia con el cliente:** el dueño ve en su historial los accesos de soporte a su negocio.
7. **Revocación inmediata:** desactivar a un miembro revoca sus sesiones, sus accesos abiertos y sus llaves.
8. **Separación de entornos:** la consola de producción solo se usa con datos de producción; las pruebas se hacen en un entorno aparte.

## Ejemplo

Un miembro de soporte necesita ver por qué no cuadra la caja de un gimnasio. Abre un acceso de lectura por 60 minutos ligado al ticket. Revisa los movimientos, responde el ticket y el acceso expira solo. El dueño ve: "Soporte NexusThrive revisó tu caja el 14 de noviembre, ticket 2026-0042".

## Decisiones relacionadas

- [ADR-0011 · La plataforma por encima de los espacios](../../05-decisiones/0011-plataforma-por-encima.md)
- [ADR-0006 · Clerk autentica, el kernel autoriza](../../05-decisiones/0006-identidad.md)
