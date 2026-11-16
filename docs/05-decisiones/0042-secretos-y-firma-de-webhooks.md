# ADR-0042 · Secretos cifrados y firma de webhooks

- **Estado:** Aceptada
- **Fecha:** 2026-11-16
- **Responsables:** santy401
- **Aceptación:** santy401, 2026-11-16, confirmada en conversación.

## Contexto

El modelo propuesto guarda `secret_hash`, pero verificar una huella no permite firmar con HMAC. El Product Owner propone cifrado reversible por entorno. HU-gateway-06 debe concretar el formato, la clave de aplicación y la ventana de rotación antes de HU-gateway-15 y 17.

## Decisión

Cada endpoint recibe un secreto aleatorio de 32 bytes generado criptográficamente. Su presentación es `whsec_` más base64url sin relleno. Se muestra solo en la respuesta inicial de creación o rotación, sobre el canal autenticado; las consultas posteriores no lo devuelven. Una respuesta perdida se resuelve con una rotación, no recuperando el secreto desde una lectura pública.

La base guarda `secret_ciphertext`, `secret_nonce` y `secret_key_id`. Se usa AES-256-GCM con una clave de aplicación de 32 bytes por entorno, nonce aleatorio nuevo de 12 bytes por cifrado y datos autenticados que vinculan versión de formato, espacio, endpoint e identificador de clave. Copiar un cifrado a otro espacio o endpoint no permite descifrarlo. El secreto nunca queda en texto plano en eventos, outbox, entregas, intentos, logs ni registros de idempotencia.

La configuración aporta un conjunto de claves por identificador y una clave activa. Un endpoint conserva el identificador usado al cifrar; cambiar la clave activa no invalida los cifrados anteriores si se conserva la clave de lectura. Cambiar la clave de aplicación requiere recifrar y comprobar los datos antes de retirar la anterior; no equivale a rotar el secreto HMAC del receptor. No se generan claves efímeras al arrancar ni se ignora un cifrado que no pueda autenticarse. El proceso con webhooks habilitados debe fallar al arrancar si falta su configuración criptográfica.

### Firma y verificación

El cuerpo JSON del evento es exactamente el conservado en el trabajo de entrega. Cada intento genera una marca de tiempo Unix en segundos y calcula HMAC-SHA256 sobre `timestamp + "." + cuerpo`, usando los 32 bytes del secreto: el receptor elimina `whsec_` y decodifica base64url antes de usarlo como clave. La cabecera es `NexusThrive-Signature: t=<timestamp>,v1=<hex>` y la solicitud usa `Content-Type: application/json`.

El receptor rechaza marcas que difieran más de cinco minutos de su reloj, verifica la firma con comparación de tiempo constante y deduplica por `event.id`. La firma autentica el cuerpo exacto; no se vuelve a serializar antes de verificar. Un reintento mantiene cuerpo e identificador del evento pero recalcula tiempo y firma. Una firma válida no autoriza a n8n a almacenar o mover valor.

### Rotación

La ventana de convivencia es de 15 minutos desde la confirmación de la rotación. Durante ella se conservan ambos secretos cifrados y se envían dos valores `v1`, uno por secreto, con la misma marca de tiempo y cuerpo. El receptor puede validar con cualquiera. Al llegar al límite solo se firma con el nuevo.

No se admite otra rotación mientras haya una ventana activa, para mantener como máximo dos secretos válidos. La respuesta muestra solo el nuevo. Los campos anteriores incluyen cifrado, nonce, identificador de clave y `previous_valid_until`; las lecturas públicas no muestran ninguno de esos valores. El material anterior puede retirarse después de la ventana sin modificar eventos o intentos históricos.

Crear y rotar son operaciones `write` con permiso `kernel.webhook.write`, sin capacidad adicional ni respuesta guardada por idempotencia genérica. De otro modo, el registro idempotente persistiría el secreto mostrado una vez. Los eventos de auditoría de esas operaciones incluyen el identificador del endpoint, nunca el secreto ni el cifrado.

## Alternativas consideradas

| Alternativa | Motivo para descartarla |
|---|---|
| Guardar solo una huella | No permite calcular HMAC |
| Guardar texto plano | Expone las credenciales de firma al leer una copia de la base |
| Rotación sin convivencia | Obliga al receptor a cambiar de clave de forma instantánea |
| Guardar la respuesta de creación para reintentos | Persiste el secreto fuera de su almacenamiento cifrado |

## Consecuencias

- El operador debe provisionar y respaldar la clave de aplicación por entorno; perderla impide recuperar secretos desde la base.
- Un proceso con acceso a esa clave puede descifrar los secretos: el cifrado protege su almacenamiento y sus copias, no un proceso comprometido.
- Los receptores deben validar marca de tiempo, una de las firmas y deduplicación. La convivencia de 15 minutos es un valor nuevo que requiere aceptación.
- No hay implementación criptográfica ni migración en esta historia; el modelo documenta la propuesta pendiente.

## Cumplimiento

Pruebas antes del código: generación de secretos distintos, ausencia de texto plano en base y registros, cifrado autenticado y nonce nuevo, rechazo al intercambiar espacio o endpoint, firma del cuerpo exacto, marca vencida o futura, cuerpo alterado, repetición con el mismo evento, límites de la ventana de doble firma, segunda rotación rechazada durante la ventana y configuración de claves incompleta. Las pruebas de permisos y aislamiento siguen siendo obligatorias.

## Referencias

- [ADR-0041 (propuesta)](0041-despacho-y-reintentos-de-webhooks.md), [modelo de datos](../02-arquitectura/modelo-de-datos/kernel.md) y [backlog](../product/backlog/m2-puertas-del-kernel.md).
- Go: [AEAD y GCM](https://pkg.go.dev/crypto/cipher) y [HMAC con comparación de tiempo constante](https://pkg.go.dev/crypto/hmac).
