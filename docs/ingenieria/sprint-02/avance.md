# Avance local del sprint S02

## Propósito

Registrar el trabajo implementado y su validación sin confundirlo con una promoción al sprint, staging ni el cierre de M2.

## Estado al 2026-11-15

| Historia | Trabajo | Rama |
|---|---|---|
| HU-gateway-01 | ADR-0037 aceptado y contrato inicial decidido | `chore/HU-gateway-01` |
| HU-gateway-02 | ADR-0038 y 0039 aceptados | `chore/HU-gateway-02` |
| HU-gateway-04 | ADR-0040 aceptado | `chore/HU-gateway-04` |
| HU-gateway-08 | Contratos, generación local Go/TypeScript y CI | `feature/HU-gateway-08` |
| HU-gateway-09 | Operaciones del contrato en el motor, atomicidad e idempotencia | `feature/HU-gateway-09` |
| HU-gateway-11 | Validación ampliada de Clerk y caché JWKS acotada | `feature/HU-gateway-11` |
| HU-gateway-10 | Servidor Connect, errores públicos, plazos y límites | `feature/HU-gateway-10` |
| HU-gateway-12 | Directorio por slug, actor desde token y cableado del binario | `feature/HU-gateway-12` |

Las ramas forman una cadena local que conserva una historia por commit y rama. El PR #27 del primer corte sigue siendo el antecedente de integración. No se ha fusionado ninguna de estas historias ni se han abierto PR nuevos durante este avance. `go.work.sum` estaba sin seguimiento al comenzar y permanece fuera de los commits.

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
