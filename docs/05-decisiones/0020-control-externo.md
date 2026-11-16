# ADR-0020 · Control externo: API, MCP, webhooks y n8n

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

NexusThrive debe poder controlarse desde afuera: integradores, agentes de IA y flujos de n8n necesitan hacer lo mismo que la interfaz, con los mismos permisos. Si cada puerta se escribe a mano, se desalinean y la interfaz acaba con atajos privados.

## Enmienda de M2

El [ADR-0044](0044-mcp-de-solo-lectura-en-m2.md) enmienda MCP y su paridad durante M2: solo herramientas generadas de riesgo `read`; ninguna de `write` o `money`. La escritura controlada queda pospuesta y exige otra decisión.

## Decisión

Cada operación nace **una sola vez** en `contracts/`. De ella se generan, sin editar a mano:

1. el método de la API (Connect-RPC, también expuesto como JSON/HTTP);
2. el cliente `@nexusthrive/sdk` que usa la interfaz;
3. la herramienta MCP equivalente para operaciones `read` en M2;
4. el evento que emite (JSON Schema), entregado por webhook firmado;
5. la operación del nodo de n8n.

La interfaz no tiene atajos: todo lo que hace la web lo puede hacer un integrador con los mismos permisos.

**Credenciales por espacio con alcances (scopes).** Las crea el dueño o el equipo de NexusThrive. Cada credencial declara los alcances que concede; el kernel autoriza con el mismo modelo de permisos de las personas. Se guardan con hash, se muestran una sola vez y se pueden revocar y rotar.

**MCP.** En M2, solo herramientas de lectura (ADR-0044). Escritura, propuestas y aprobación humana quedan pospuestas; ni siquiera una credencial con alcance de escritura las habilita.

**Webhooks.** Firmados con HMAC, con reintentos con espera creciente, registro de entregas, reenvío manual y rotación del secreto de firma.

**n8n.** Nodo generado desde el contrato, con disparador (trigger) por eventos. Se publican plantillas de flujos: para la operación de NexusThrive (prueba por vencer, pago recibido, alerta de existencias bajas) y para el dueño de la tienda (resumen diario por correo, alerta de cartera vencida).

**Reglas de toda escritura externa:** clave de idempotencia, alcance de su credencial y registro de auditoría con quién, qué, cuándo y desde dónde.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Escribir a mano cada puerta | Se desalinean con el tiempo y la interfaz acaba teniendo atajos. |
| MCP de solo lectura | Elegido para M2 por S-P1 y ADR-0044; la escritura se reconsiderará después. |
| MCP con escritura libre | Un agente podría mover dinero o existencias sin que nadie lo apruebe. |
| Webhooks sin firma ni registro | No se puede verificar el origen ni depurar entregas fallidas. |

## Consecuencias

### Positivas

- Un solo lugar para añadir una operación y que aparezca en todas las puertas.
- Auditoría uniforme de lo que entra desde afuera.

### Negativas

- El generador del contrato es crítico: un error se propaga a todas las puertas.

### Riesgos

- Credenciales filtradas. Mitigación: alcances mínimos, revocación inmediata, límites de tasa y registro.
- Webhooks duplicados por reintentos. Mitigación: cada evento lleva un identificador estable para que el receptor lo deduplique.

## Cumplimiento

- Una **prueba de arquitectura** comprueba la paridad por puerta: MCP solo incluye las operaciones `read`; API, SDK y n8n siguen el contrato y los eventos publicados tienen esquema.
- Una prueba comprueba paridad de lecturas por API, MCP y n8n, e idempotencia de escrituras por las puertas habilitadas.
- Una prueba comprueba que toda escritura por MCP se rechaza sin ejecutar el motor durante M2.

## Referencias

- [ADR-0004](0004-contrato-unico.md)
- [ADR-0005](0005-integraciones.md)
- [ADR-0006](0006-identidad.md)
- [Puertas](../04-integraciones/puertas.md)
