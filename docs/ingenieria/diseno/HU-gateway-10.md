# HU-gateway-10 · Gateway Connect-RPC

## Propósito y alcance

Exponer el servicio generado de `kernel.v1` sobre HTTP con un autenticador inyectable. El servidor recibe el motor, nunca el pool ni `kernel/state`. La composición con el directorio real por slug queda en HU-gateway-12; no se expone un selector temporal por UUID en estas rutas.

## Diseño

La envoltura HTTP identifica las dos rutas públicas, impone un plazo de contexto y admite solicitudes según IP de conexión antes de autenticar. El autenticador devuelve actor, UUID del espacio y un identificador de credencial verificados; se rechazan actor nulo o `system` y credencial vacía. Después se aplica el límite de credencial y se conserva el cuerpo original, acotado a 1 MiB, en el contexto privado de la llamada. Los manejadores generados reciben el cuerpo restaurado para decodificarlo, sin que esa decodificación sustituya los bytes del motor.

El servidor Connect llama exclusivamente `Engine.WriteExchange` y `Engine.ReadEvents`. La respuesta idempotente se convierte al mensaje generado y conserva la señal de repetición como cabecera. La lectura convierte el evento y su payload al mensaje del contrato. La traducción de errores publica solo código y mensaje del dominio en `OperationError`; causas desconocidas y pánicos son `internal` genérico.

Los límites iniciales son 10 segundos por llamada y 120 peticiones por minuto por IP y credencial, con 10000 contadores como máximo y reloj inyectable. El mapa se reinicia al comenzar una nueva ventana de minuto. Los límites se configuran al construir el servidor y no usan Redis. Una tabla llena rechaza nuevos contadores hasta la siguiente ventana, sin crecer indefinidamente.

## Pruebas

Las pruebas se escribieron antes del constructor y los tipos del servidor, y fallaron por su ausencia. Comprueban escritura y repetición por HTTP, falta de clave, conflicto, huella calculada sobre los nombres originales de campos JSON, autenticación, vencimiento de contexto, límite y recuperación de ventana, cabecera de espera, rechazo de dinero binario y ausencia de rutas del outbox.

Una prueba independiente cubre cada código de error del dominio, su detalle generado y la ausencia de causas internas o valores de pánico en la respuesta. La cabecera afirmada `Actor-System` no cambia la autoridad del autenticador. La prueba de arquitectura mantiene la puerta sin importaciones de `state`.

## Referencias

- [Puertas](../../04-integraciones/puertas.md).
- [ADR-0037](../../05-decisiones/0037-contrato-inicial-del-kernel.md), [ADR-0038](../../05-decisiones/0038-resolucion-de-actor-y-espacio.md), [ADR-0039](../../05-decisiones/0039-identidad-en-el-primer-ingreso.md) y [ADR-0040](../../05-decisiones/0040-validacion-de-clerk-y-cache-jwks.md).
