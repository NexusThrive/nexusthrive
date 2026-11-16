# Avance y PR del sprint S02

## Propósito

Registrar el trabajo implementado y su validación sin confundirlo con una promoción al sprint, staging ni el cierre de M2.

## Estado al 2026-11-16

| Historia | Trabajo | Rama | PR | Depende de |
|---|---|---|---|---|
| HU-gateway-01 | ADR-0037 aceptado y contrato inicial decidido | `chore/HU-gateway-01` | [#28](https://github.com/NexusThrive/nexusthrive/pull/28) | #27 |
| HU-gateway-08 | Contratos, generación local Go/TypeScript y CI | `feature/HU-gateway-08` | [#29](https://github.com/NexusThrive/nexusthrive/pull/29) | #28 |
| HU-gateway-02 | ADR-0038 y 0039 aceptados | `chore/HU-gateway-02` | [#30](https://github.com/NexusThrive/nexusthrive/pull/30) | #29 |
| HU-gateway-09 | Operaciones del contrato en el motor, atomicidad e idempotencia | `feature/HU-gateway-09` | [#31](https://github.com/NexusThrive/nexusthrive/pull/31) | #30 |
| HU-gateway-04 | ADR-0040 aceptado | `chore/HU-gateway-04` | [#32](https://github.com/NexusThrive/nexusthrive/pull/32) | #31 |
| HU-gateway-11 | Validación ampliada de Clerk y caché JWKS acotada | `feature/HU-gateway-11` | [#33](https://github.com/NexusThrive/nexusthrive/pull/33) | #32 |
| HU-gateway-10 | Servidor Connect, errores públicos, plazos y límites | `feature/HU-gateway-10` | [#34](https://github.com/NexusThrive/nexusthrive/pull/34) | #33 |
| HU-gateway-12 | Directorio por slug, actor desde token y cableado del binario | `feature/HU-gateway-12` | [#35](https://github.com/NexusThrive/nexusthrive/pull/35) | #34 |
| HU-gateway-06 | ADR-0041 y 0042 aceptados: despacho y secretos de webhooks | `chore/HU-gateway-06` | [#36](https://github.com/NexusThrive/nexusthrive/pull/36) | #35 |
| HU-gateway-07 | ADR-0043 aceptado: ciclo, cotas y apagado del programador | `chore/HU-gateway-07` | [#37](https://github.com/NexusThrive/nexusthrive/pull/37) | #36 |
| HU-gateway-15 | Registro Connect, cifrado, firma y colas duraderas con RLS | `feature/HU-gateway-15` | [#38](https://github.com/NexusThrive/nexusthrive/pull/38) | #37 |

Las once ramas están publicadas y sus PR están en borrador, cada uno con la rama precedente como base temporal. El [PR #27](https://github.com/NexusThrive/nexusthrive/pull/27) del primer corte sigue siendo el antecedente de integración. El destino final es `sprint/S02`; después de integrar cada antecedente se actualiza la rama, se cambia la base y se revisan diff y CI. No se ha fusionado ninguna historia. `go.work.sum` estaba sin seguimiento al comenzar y permanece fuera de los commits.

La primera ejecución de CI de los ocho PR #28 a #35 terminó correctamente, comprobada el 2026-11-15 a las 23:48 UTC. Los checks vigentes de cada nueva cabecera se consultan en su PR; cualquier actualización vuelve a comprobarse antes de revisión. El modo de publicación y seguimiento está fijado en [flujo de Git](../../07-ingenieria/git-workflow.md) y `AGENTS.md`.

## Validación

- Pruebas escritas antes de operaciones, esquemas y adaptadores, con fallos iniciales observados y posterior ejecución contra PostgreSQL del proyecto en 55432.
- Lint de Go, TypeScript y contratos; suite completa con arquitectura y las cinco leyes; compilación de todos los módulos.
- Pruebas de política del contrato, precisión, JSON Schema, cambio incompatible real y generación repetible.
- Detector de carreras sobre las pruebas nuevas de tokens y JWKS.
- Render de los 58 diagramas con la configuración de CI para el servidor.

Se utiliza Go 1.24.7, porque el Staticcheck fijado no admite Go 1.27 del servidor. Task se ejecuta mediante `go run github.com/go-task/task/v3/cmd/task@v3.40.1`.

## Lo que todavía falta

M2 sigue abierto: decisiones y construcción de webhooks, llaves por espacio, MCP de lectura, nodo n8n, proceso programador, imagen y arranque, prueba completa del criterio B3 y staging. El token real de Clerk y el despliegue dependen de las cuentas y accesos del responsable. No hay consola ni producto Business implementados por este avance.

La migración 00015 pertenece al directorio; las historias de llaves y webhooks deben reservar números posteriores. La API debe tener aplicadas las migraciones antes de servir las rutas Connect. Los métodos del outbox siguen fuera de la API pública.

## Referencias

- [Plan](../../product/plan.md) y [backlog](../../product/backlog/m2-puertas-del-kernel.md).
- [Hoja de ruta](../../02-arquitectura/hoja-de-ruta.md).

Los PR #36 y #37 pasaron CI antes de su aceptación. santy401 aceptó ADR-0041, 0042 y 0043 el 2026-11-16 en conversación. Sus actualizaciones registran la aceptación; HU-gateway-15 queda habilitada.

HU-gateway-15 pasó lint, suite completa con leyes, compatibilidad del contrato, regeneración, detector de carreras de las pruebas nuevas y 58 diagramas. CI del PR #38 se comprueba tras cada push. Reintentos operativos y deshabilitación siguen en HU-gateway-16; reenvío y rotación en 17; arranque automático en 21.
