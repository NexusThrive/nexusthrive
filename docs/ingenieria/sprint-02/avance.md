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
| HU-gateway-16 | Reintentos, veinte fallos consecutivos y motivo consultable | `feature/HU-gateway-16` | [#39](https://github.com/NexusThrive/nexusthrive/pull/39) | #38 |
| HU-gateway-03 | ADR-0044 registra S-P1 aceptado y enmienda paridad MCP | `chore/HU-gateway-03` | [#40](https://github.com/NexusThrive/nexusthrive/pull/40) | #39 |
| HU-gateway-05 | ADR-0045 aceptado: llaves y alcances transaccionales | `chore/HU-gateway-05` | [#41](https://github.com/NexusThrive/nexusthrive/pull/41) | #40 |

Los PR #27 a #43 están fusionados en `sprint/S02`, con autorización explícita del responsable el 2026-11-16. Cada uno pasó CI; antes de integrar se comprobó que el árbol candidato coincidía con el head validado y se cambió la base al sprint. `main` no se modificó. ADR-0045 está aceptado. La tabla anterior conserva las dependencias históricas, no PR pendientes. `go.work.sum` continúa fuera de los commits.

HU-gateway-05 está integrada en [#41](https://github.com/NexusThrive/nexusthrive/pull/41); reserva 00018 para llaves. HU-gateway-17 está integrada con migración 00017; HU-gateway-21 está integrada en #43. Los PR nuevos se fusionan al pasar CI, según la autorización registrada en `AGENTS.md` y el flujo de Git.

## Validación

- Pruebas escritas antes de operaciones, esquemas y adaptadores, con fallos iniciales observados y posterior ejecución contra PostgreSQL del proyecto en 55432.
- Lint de Go, TypeScript y contratos; suite completa con arquitectura y las cinco leyes; compilación de todos los módulos.
- Pruebas de política del contrato, precisión, JSON Schema, cambio incompatible real y generación repetible.
- Detector de carreras sobre las pruebas nuevas de tokens y JWKS.
- Render de los 58 diagramas con la configuración de CI para el servidor.

Se utiliza Go 1.24.7, porque el Staticcheck fijado no admite Go 1.27 del servidor. Task se ejecuta mediante `go run github.com/go-task/task/v3/cmd/task@v3.40.1`.

## Lo que todavía falta

M2 sigue abierto: llaves (13), MCP de lectura (14), nodo n8n (19), imagen y arranque (20) y prueba completa del criterio B3 (18). El token real de Clerk (22) y staging (23) están diferidos porque el responsable indicó completar primero desarrollo y pruebas locales. No hay consola ni producto Business implementados por este avance.

Las migraciones 00015, 00016 y 00017 pertenecen al directorio, webhooks y rotación/reenvío. 00018 queda reservada para llaves. Los métodos de reclamo y marcado siguen fuera de la API pública.

Las claves de Clerk están disponibles en `.env`; la conexión de Supabase está en un archivo local ignorado, sin publicarla. Dokploy y n8n existen en el servidor con Tailscale privado. Cloudflare está elegido para la publicación futura; Render se evalúa como alternativa para la API. No se han cambiado DNS ni puertos del router, ni migrado la base externa. El alcance actual sigue siendo local.

## Referencias

- [Plan](../../product/plan.md) y [backlog](../../product/backlog/m2-puertas-del-kernel.md).
- [Hoja de ruta](../../02-arquitectura/hoja-de-ruta.md).

HU-gateway-20 continúa en el [PR #44](https://github.com/NexusThrive/nexusthrive/pull/44). santy401 aceptó el ADR-0046 el 2026-11-16: el rol global sigue NOLOGIN y se implementará un login limitado por despliegue con SCRAM y bootstrap sin secretos privilegiados durante el servicio. La historia sigue en curso hasta completar implementación y pruebas locales.
