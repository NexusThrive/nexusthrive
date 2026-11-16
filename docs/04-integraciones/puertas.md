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

### API Connect de M2

HU-gateway-10 implementa el servidor generado `kernel.v1.KernelService` mediante `gateway.NewConnectServer`. Recibe solo el motor y un autenticador que entrega identidad de credencial, participante y espacio verificados; no recibe conexiones ni el pool. HU-gateway-12 conecta el token verificado con el directorio del motor por slug y monta las rutas en el binario. El corte HTTP por UUID del ADR-0036 permanece solo en desarrollo; producción ofrece las operaciones del contrato y salud. Antes de servir la API deben estar aplicadas las migraciones, incluida la 00015 del directorio.

El cliente Go generado debe construirse con `connect.WithProtoJSON()` para la escritura de dinero; el protocolo binario por defecto del generador no está habilitado para esa operación en M2.

| Ruta Connect | Operación |
|---|---|
| `/{slug}/kernel.v1.KernelService/WriteExchange` | Intercambio y compromiso atómicos, idempotencia obligatoria |
| `/{slug}/kernel.v1.KernelService/ReadEvents` | Lectura autorizada de eventos por cursor inclusivo |

La escritura usa Connect con JSON sin compresión de solicitud; otros formatos de dinero se rechazan antes de ejecutar. El cuerpo original llega a `Call.Request` sin reconstruirse desde el mensaje Protobuf. `Idempotency-Key` y el campo `idempotency_key` deben coincidir si aparecen ambos; falta de clave es `invalid_input`. La repetición incluye `Idempotent-Replayed: true` fuera del cuerpo almacenado.

El contexto tiene un máximo de 10 segundos. Por defecto se permiten 120 peticiones por minuto por IP y por credencial validada, con reloj inyectable y un máximo de 10000 contadores en memoria. La IP procede de la conexión, sin confiar en `X-Forwarded-For`. El exceso responde `resource_exhausted` con `Retry-After`. Las solicitudes no superan 1 MiB. Estos valores se pueden configurar al construir el servidor; no requieren Redis en M2.

| Código del dominio | Código Connect |
|---|---|
| `invalid_input` | `invalid_argument` |
| `permission_denied` | `permission_denied` |
| `not_found` | `not_found` |
| `already_done`, `idempotency_conflict` | `already_exists` |
| `rule_violation`, `capability_inactive`, `space_suspended` | `failed_precondition` |
| `retryable` | `aborted` |

El código del dominio y su mensaje público viajan en el detalle `OperationError`; la causa SQL nunca sale en el detalle. Autenticación fallida produce `unauthenticated`, vencimiento de contexto `deadline_exceeded` y cancelación `canceled`. Los errores desconocidos y `PanicError` responden `internal` genérico. Reclamo y marcadores del outbox no tienen rutas públicas. Ninguna cabecera o campo de la operación puede construir un `ActorSystem`.

## Reglas e invariantes

- Agregar una puerta no duplica lógica: todas llaman a las mismas operaciones del contrato.
- Al principio el MCP solo expone operaciones de lectura; las de escritura se habilitan por alcance cuando se aprueben.
- Un endpoint de webhook se deshabilita solo después de 20 entregas fallidas seguidas y aparece en la consola.
