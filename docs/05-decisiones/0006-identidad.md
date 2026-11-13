# 0006 · Clerk prueba identidad; el kernel decide permisos

**Estado:** aceptada

## Decisión
Clerk maneja inicio de sesión, verificación, doble factor y recuperación. El kernel valida el token y guarda participantes, roles y permisos.

## Consecuencias
- No construimos autenticación propia ahora.
- Cambiar a una alternativa abierta (Zitadel, Ory) solo cambia quién emite el token.
