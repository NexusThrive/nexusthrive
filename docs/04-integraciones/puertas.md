# Puertas del kernel

## Propósito

Describir los puntos de entrada y salida del kernel.

## Alcance

API, MCP, webhooks salientes y entrantes. Todas pasan por el mismo motor de operaciones.

## Funcionamiento

| Puerta | Para quién | Cómo se protege |
|---|---|---|
| **API** (Connect) | Apps, consola, n8n, terceros | Token de Clerk o llave por espacio, alcances, límites por minuto, idempotencia |
| **MCP** | Agentes de IA propios o de terceros | La misma llave y permisos; el agente es un participante con identidad propia |
| **Webhooks salientes** | Terceros que quieren enterarse de eventos | Firmados con el secreto del endpoint, con reintentos y versión del evento |
| **Webhooks entrantes** | Pasarelas y servicios externos | Firma verificada; un aviso nunca se procesa dos veces |

## Reglas e invariantes

- Agregar una puerta no duplica lógica: todas llaman a las mismas operaciones del contrato.
- Al principio el MCP solo expone operaciones de lectura; las de escritura se habilitan por alcance cuando se aprueben.
- Un endpoint de webhook se deshabilita solo después de 20 entregas fallidas seguidas y aparece en la consola.
