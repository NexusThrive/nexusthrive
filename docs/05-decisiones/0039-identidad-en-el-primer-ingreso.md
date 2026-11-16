# ADR-0039 · Primer ingreso sin creación automática de identidad

- **Estado:** Aceptada
- **Fecha:** 2026-11-15
- **Responsables:** santy401
- **Aceptación:** santy401, 2026-11-15

## Contexto

Un token válido de Clerk prueba el control de una identidad externa, pero no su pertenencia a un espacio. El seguimiento 6 de M1 advierte que intentar insertar identidades y traducir errores de unicidad o referencias permite revelar su existencia. El seguimiento 4 exige resolver `IdentityID` en servidor, nunca desde un cliente.

El corte aceptado por el ADR-0036 ya rechaza un token sin participante activo. HU-gateway-02 debe dejar ese comportamiento como política explícita para el contrato definitivo de M2.

## Decisión

El ingreso por API o MCP no crea ni actualiza identidades, participantes, roles ni grants. El proveedor y subject de la credencial validada se resuelven contra registros previamente preparados por el proceso interno de arranque o sembrado. El alta y las invitaciones serán operaciones posteriores, con su propia decisión y permisos.

Una identidad válida sin participante activo en el espacio obtiene `permission_denied` con el mensaje público `the identity has no access to this space`, igual que una identidad desconocida, deshabilitada, con participante archivado o ajena al slug. Un participante existente sin el permiso de la operación recibe el mismo mensaje público de denegación. El detalle de la verificación queda en registros internos sin tokens ni datos personales.

Una credencial inválida se rechaza antes con `unauthenticated`. Un fallo de infraestructura conserva su clasificación interna y llega como error genérico de infraestructura; no se usa para fingir que falta una identidad. No se propagan `23505`, `23503`, nombres de restricciones, correos ni UUID de terceros al cliente.

`IdentityID` enviado por un cliente no se usa para vincular participantes ni construir el actor. Los identificadores de deudor y acreedor en `WriteExchange` son referencias del compromiso, no credenciales; el motor los valida dentro del espacio después de verificar al actor y sus permisos.

La búsqueda sin alta utiliza el recorrido del ADR-0038 y los límites de peticiones de HU-gateway-10. El ingreso no revela si falta la identidad o solo su pertenencia. No se promete igualar exactamente tiempos de operaciones autorizadas y denegadas; se elimina la consulta de existencia independiente y se comprueban las formas de respuesta y los resultados vacíos.

## Alternativas consideradas

| Alternativa | Motivo para descartarla |
|---|---|
| Crear identidad y participante al primer token válido | Concede pertenencia por conocer un slug y amplía M2 sin un flujo de invitación |
| Crear únicamente la identidad al ingresar | Introduce una escritura no necesaria y errores de existencia antes de la autorización |
| Devolver si falta identidad o participante | Permite enumerar identidades o espacios |

## Consecuencias

- No se produce aprovisionamiento implícito durante la autenticación.
- Las pruebas locales y la prueba real con Clerk requieren preparar la identidad y su participante antes de la llamada.
- HU-gateway-09 y HU-gateway-12 no incluyen invitaciones ni altas; HU-gateway-22 debe comprobar también un token real sin pertenencia.
- La creación interna existente de M1 no se expone por una ruta nueva ni se declara resuelto el alta futura del seguimiento 6.

## Cumplimiento

Pruebas con token válido y base sin identidad, identidad sin participante y participante archivado: mismo `permission_denied` y ninguna fila creada o actualizada. Pruebas con participante sin grant y con grant revocado: misma respuesta pública. Pruebas de identidad y espacio afirmados en la solicitud, dos espacios aislados y contexto cancelado. Las leyes del kernel y las pruebas de arquitectura permanecen obligatorias.

## Referencias

- [ADR-0006](0006-identidad.md), [ADR-0035](0035-motor-de-ejecucion.md), [ADR-0036](0036-primera-puerta-http-del-kernel.md) y [ADR-0038 (propuesta)](0038-resolucion-de-actor-y-espacio.md).
- [Seguimientos 4 y 6 de M1](../ingenieria/sprint-01/seguimientos.md).
- [Backlog M2](../product/backlog/m2-puertas-del-kernel.md).
