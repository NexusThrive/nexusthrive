# ADR-0044 · MCP de solo lectura durante M2

- **Estado:** Aceptada
- **Fecha:** 2026-11-16
- **Responsables:** santy401
- **Aceptación:** registra S-P1, ya confirmado por santy401 en el traspaso; no introduce un alcance nuevo.
- **Enmienda a:** ADR-0018 y ADR-0020

## Contexto

El responsable ya decidió S-P1: M2 ofrece MCP de solo lectura. ADR-0018 y ADR-0020 todavía incluyen lectura y escritura controlada, lo que contradice esa decisión y permite generar herramientas fuera del alcance. HU-gateway-03 registra la enmienda antes de implementar HU-gateway-14 y 19.

## Decisión

En M2, `/mcp` ofrece una herramienta generada por cada operación cuyo riesgo declarado en `contracts/` sea `read`. No ofrece operaciones `write` ni `money`, aunque la credencial tenga esos alcances. Tampoco hay propuestas de escritura, aprobación humana, recursos ni prompts avanzados en esta fase. La escritura por MCP queda pospuesta y requerirá otro ADR, sin fecha de M2.

Las herramientas usan el mismo motor y los permisos de la credencial validada, ligada al participante del agente y a su espacio. No reciben conexiones de base, no construyen un actor de sistema y no descubren espacios por una entrada del cliente sin comprobar su credencial. Tener una herramienta de lectura no evita la comprobación de permiso ni la pertenencia al espacio.

La paridad de ADR-0020 se comprueba por riesgo y puerta: toda operación pública `read` debe existir en el MCP generado; la ausencia de una `write` o `money` en MCP es obligatoria y no un fallo de paridad. API y SDK conservan el alcance del contrato. n8n sigue las restricciones de su historia y nunca almacena ni mueve valor; esta enmienda no concede una excepción a esa regla.

La generación y las pruebas deben fallar si se agrega a MCP una operación de escritura o si falta una operación de lectura. Invocar por nombre una herramienta no generada se rechaza sin ejecutar el motor.

## Alternativas consideradas

| Alternativa | Motivo para descartarla |
|---|---|
| Mantener escritura controlada durante M2 | Contradice S-P1 y exige construir propuestas y aprobación |
| Generar herramientas manuales de lectura | Permite que la puerta diverja del contrato |
| Confiar en los alcances para ocultar escrituras | Una credencial con alcance amplio podría habilitar operaciones pospuestas |

## Consecuencias

- Se registra una decisión del responsable ya tomada, sin solicitar una nueva aceptación del mismo alcance.
- MCP no sirve para mover valor ni configurar el espacio durante M2.
- Los generadores y las pruebas de paridad filtran por riesgo; los límites del motor y de autenticación siguen vigentes.

## Cumplimiento

Pruebas de paridad que agregan y quitan operaciones de cada riesgo; llamadas a nombres de escritura rechazadas; acceso sin llave, con llave revocada o fuera de alcance denegado; aislamiento por espacio y ausencia de operaciones internas en herramientas. Las cinco leyes permanecen activas.

## Referencias

- [ADR-0018](0018-recorte-de-la-etapa-1.md), [ADR-0020](0020-control-externo.md), [ADR-0023](0023-urls-y-dominios.md).
- [Puertas](../04-integraciones/puertas.md), [traspaso](../ingenieria/traspaso-codex.md) y [backlog](../product/backlog/m2-puertas-del-kernel.md).
