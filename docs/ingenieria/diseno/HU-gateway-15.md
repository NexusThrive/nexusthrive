# HU-gateway-15 · Webhooks firmados y reparto duradero

## Alcance y decisión

Implementa los ADR-0041 y 0042 aceptados por santy401 el 2026-11-16. El ADR-0043 también está aceptado: el arranque periódico de timers y despacho sigue en HU-gateway-21. La migración 00016 queda reservada a esta historia; 00015 sigue perteneciendo al directorio de actor.

## Camino implementado

- Contrato Connect `CreateWebhookEndpoint`, sin idempotencia genérica, y `ListWebhookDeliveries`, con permisos `kernel.webhook.write` y `kernel.webhook.read`. La consulta devuelve metadatos, nunca secretos ni el cuerpo del evento.
- Creación con secreto aleatorio de 32 bytes, presentado una sola vez como `whsec_…`, respuesta `Cache-Control: no-store` y rechazo de claves de idempotencia. Se conserva cifrado AES-256-GCM con nonce aleatorio y AAD de versión, espacio, endpoint e identificador de clave.
- Registro HTTPS y validación de todas las direcciones DNS. El transporte vuelve a resolver y validar antes del dial, fija la IP pública, mantiene el hostname para TLS, no usa proxy de entorno ni sigue redirecciones. Desarrollo permite únicamente un receptor HTTP con IP literal de loopback.
- Migración con endpoints, entregas y un historial de intentos de solo anexar. Las tres tablas tienen RLS, claves compuestas por espacio y restricciones de datos. La prueba de aislamiento llena las tablas mediante sus operaciones reales.
- Intercambios y vencimientos escriben sus filas `internal` y `webhooks` junto con el evento. Se conserva el suscriptor interno sin fingir que existe un consumidor.
- Reparto con comprobación del token de reclamo: crea todos los trabajos y confirma `sent` del outbox en la misma transacción. `sent` acredita aceptación duradera del reparto; cada recibo HTTP se consulta por entrega. La unicidad por espacio, endpoint y evento evita duplicar trabajos.
- Cuerpo fijado como bytes al materializar el trabajo, secuencia decimal como cadena y `event.id` estable. El HMAC-SHA256 firma `timestamp.cuerpo` y se envía en `NexusThrive-Signature`.
- HTTP fuera de la transacción, timeout de cinco segundos y arrendamiento de cinco minutos. La finalización exige el token vigente y no vencido. Una caída puede repetir un POST; el receptor deduplica por `event.id`.
- Directorio interno limitado a diez UUID de espacios activos por página. Su rol NOLOGIN no tiene BYPASSRLS, acceso a otras tablas ni membresía para la API. Las excepciones de SECURITY DEFINER admiten solamente las dos firmas decididas en ADR-0038 y 0041.
- `DispatchPage` rota con cursor y continúa después de fallos locales. El programador de HU-gateway-21 controlará plazos globales, señales y ausencia de pasadas superpuestas.

La base de entrega conserva cinco intentos y recuperación de reclamos vencidos con historial. HU-gateway-16 completa la política operativa del contador consecutivo, la deshabilitación y su cobertura de reintentos. HU-gateway-17 añade reenvío manual y doble secreto durante la rotación; esta historia no los anuncia disponibles.

## Configuración

Las rutas nuevas están ocultas por defecto. Para habilitarlas se exige `NEXUSTHRIVE_WEBHOOKS_ENABLED=1`, `WEBHOOK_ENCRYPTION_ACTIVE_KEY` y `WEBHOOK_ENCRYPTION_KEYS` (objeto JSON de identificadores a claves de aplicación de 32 bytes en base64 estándar). Una clave ausente, corta o mal codificada impide el arranque sin imprimir su valor. No se generan claves efímeras de aplicación. El anillo permite conservar claves antiguas para descifrar; no realiza recifrado automático.

Fuera del modo de desarrollo existente no hay permiso de loopback. Las credenciales reales se inyectan como secretos de entorno; `.env.example` contiene únicamente campos vacíos. Los espacios existentes deben conceder los permisos nuevos explícitamente; solo el rol propietario de espacios nuevos los incorpora.

## Validación y límites

Se observaron fallos antes de implementar cifrado, motor, configuración y publicación del segundo suscriptor. Pruebas contra PostgreSQL real y receptor local comprueban registro, firma de bytes exactos, aislamiento, reparto repetido, rollback inyectado, reclamo exclusivo, resultado tardío rechazado, historial y consulta sin secreto. Las pruebas de red incluyen DNS mixto y cambio a loopback entre registro y conexión; el cifrado rechaza intercambiar espacio, endpoint o contenido autenticado.

Se mantienen activas las cinco leyes. Las expectativas de timers cambian de una a dos filas de outbox y verifican ambos suscriptores. La prueba de memoria elimina todas las referencias externas antes de comprobar el trigger de eventos, para que una nueva FK no oculte el resultado. No se omite ninguna prueba ni se amplía el acceso normal de la API.

No hay staging, token real de Clerk ni despacho automático todavía. El cierre de M2 requiere sus historias de integración, el ciclo de HU-gateway-21 y las cuentas externas.

Validación final: `task lint`, `task test`, compatibilidad Protobuf frente a `chore/HU-gateway-07`, regeneración repetible y 58 diagramas pasaron. Las pruebas nuevas de motor y puerta también pasaron con `-race`. Se ejecutaron con Go 1.24.7 y PostgreSQL del proyecto en 55432.
