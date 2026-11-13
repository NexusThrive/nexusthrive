# ADR-0006 · Clerk autentica; el kernel autoriza

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

Construir autenticación propia (verificación, doble factor, recuperación, sesiones) es costoso y riesgoso.

## Decisión

Clerk prueba quién es la persona y entrega un token. El kernel valida el token y guarda identidades, participantes, roles y permisos.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Autenticación propia | Meses de trabajo y riesgo de seguridad. |
| Permisos dentro de Clerk | Ata el modelo de permisos a un proveedor. |

## Consecuencias

### Positivas

- Seguridad de autenticación madura desde el primer día.
- Cambiar de proveedor solo cambia quién emite el token.

### Negativas

- Dependencia de un proveedor externo y su costo por usuario.

### Riesgos

- Caída del proveedor impide iniciar sesión.

## Cumplimiento

Ninguna tabla de permisos depende de datos de Clerk más allá de `identities.subject`.

## Referencias

- [Identidad](../04-integraciones/identidad.md)
