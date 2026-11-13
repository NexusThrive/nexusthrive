# 0004 · Un contrato único

**Estado:** aceptada

## Decisión
Las operaciones se definen en Protobuf (Connect-RPC) y los eventos en JSON Schema, dentro de `contracts/`. De ahí se generan el servidor Go, los SDK de TypeScript y Python y las herramientas MCP.

## Consecuencias
- Nada se escribe a mano en los extremos.
- Un cambio que rompe compatibilidad se detecta antes de integrarse.
- Toda operación declara permiso, capacidad, riesgo e idempotencia.
