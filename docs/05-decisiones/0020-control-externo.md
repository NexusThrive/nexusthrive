# ADR-0020 · Control externo: API, MCP, webhooks y n8n

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

NexusThrive debe poder controlarse desde afuera: integradores, agentes de IA y flujos de n8n necesitan hacer lo mismo que la interfaz, con los mismos permisos. Si cada puerta se escribe a mano, se desalinean y la interfaz acaba con atajos privados.

## Decisión

Cada operación nace **una sola vez** en `contracts/`. De ella se generan, sin editar a mano:

1. el método de la API (Connect-RPC, también expuesto como JSON/HTTP);
2. el cliente `@nexusthrive/sdk` que usa la interfaz;
3. la herramienta MCP equivalente;
4. el evento que emite (JSON Schema), entregado por webhook firmado;
5. la operación del nodo de n8n.

La interfaz no tiene atajos: todo lo que hace la web lo puede hacer un integrador con los mismos permisos.

**Credenciales por espacio con alcances (scopes).** Las crea el dueño o el equipo de NexusThrive. Cada credencial declara los alcances que concede; el kernel autoriza con el mismo modelo de permisos de las personas. Se guardan con hash, se muestran una sola vez y se pueden revocar y rotar.

**MCP.** Herramientas de lectura y de escritura controlada. Una escritura que mueve dinero o existencias llega como **propuesta** que una persona aprueba, salvo que la credencial tenga un alcance de escritura explícito otorgado por el dueño.

**Webhooks.** Firmados con HMAC, con reintentos con espera creciente, registro de entregas, reenvío manual y rotación del secreto de firma.

**n8n.** Nodo generado desde el contrato, con disparador (trigger) por eventos. Se publican plantillas de flujos: para la operación de NexusThrive (prueba por vencer, pago recibido, alerta de existencias bajas) y para el dueño de la tienda (resumen diario por correo, alerta de cartera vencida).

**Reglas de toda escritura externa:** clave de idempotencia, alcance de su credencial y registro de auditoría con quién, qué, cuándo y desde dónde.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Escribir a mano cada puerta | Se desalinean con el tiempo y la interfaz acaba teniendo atajos. |
| MCP de solo lectura | Impide que agentes y AURI ayuden a operar. |
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

- Una **prueba de arquitectura** falla si una operación existe en la API pero no en MCP, webhooks o n8n, o al revés.
- Una prueba comprueba que una operación de prueba se ejecuta igual por API, MCP y n8n, y que repetirla con la misma clave no la duplica.
- Una prueba comprueba que una escritura por MCP que mueve dinero queda como propuesta cuando la credencial no tiene alcance explícito.

## Referencias

- [ADR-0004](0004-contrato-unico.md)
- [ADR-0005](0005-integraciones.md)
- [ADR-0006](0006-identidad.md)
- [Puertas](../04-integraciones/puertas.md)
