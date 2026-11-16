# HU-gateway-17 · Reenvío y rotación de webhooks

## Comportamiento

La migración 00017 añade el secreto anterior cifrado y su vencimiento al endpoint, y `replay_of` a los trabajos. Una restricción exige todos los campos anteriores juntos o ninguno. La unicidad del evento original sigue vigente mediante un índice parcial; los reenvíos tienen identificadores nuevos y una FK compuesta al trabajo fuente del mismo espacio.

`RotateWebhookSecret` exige `kernel.webhook.write`, sustituye el secreto actual y conserva el anterior durante quince minutos. La respuesta muestra únicamente la presentación nueva, con `Cache-Control: no-store`; se rechaza una clave de idempotencia para impedir que el secreto termine en su registro. Otra rotación durante una ventana activa falla con `rule_violation`; en el límite exacto vuelve a permitirse. Rotar un endpoint deshabilitado no lo reactiva.

Cada entrega firma los bytes guardados con el secreto actual y, mientras `now < previous_valid_until`, añade una segunda firma `v1` con el secreto anterior. Ambas comparten marca de tiempo y cuerpo. Al llegar al límite solo se usa la actual y ya no hace falta conservar la clave de aplicación antigua para descifrar un secreto anterior vencido. Un secreto anterior todavía vigente que no se puede autenticar produce error; no se ignora en silencio.

`ReplayWebhookDelivery` exige escritura y una clave de idempotencia. Solo admite trabajos fallidos o muertos sin reclamo vigente y un endpoint activo. Crea otro trabajo, conserva el cuerpo y `event.id`, y mantiene intactos el original y su historial. La respuesta idempotente contiene solamente el nuevo identificador, nunca secretos. La API exige JSON sin compresión para conservar el cuerpo original de esta petición idempotente, igual que para la escritura del intercambio.

Crear endpoint, rotar secreto y reenviar agregan eventos de auditoría en la misma transacción, con actor y solicitud fijados por el motor. Sus esquemas están en `contracts/events/`; contienen solo identificadores seguros. No se publican el secreto, su cifrado ni la respuesta de presentación única como payload.

## Pruebas y compatibilidad

Se observaron fallos antes de implementar motor y puerta. Pruebas con PostgreSQL real cubren convivencia, segunda rotación denegada, límite exacto, permiso, aislamiento, endpoint deshabilitado, repetición idempotente, identidad estable del evento y conservación del historial. La prueba de firma comprueba ambos HMAC sobre el mismo cuerpo y el retiro exacto de la firma anterior; la ruta HTTP verifica presentación única y rechazo de idempotencia en rotación.

El rol de API no cambia y la puerta sigue recibiendo solo `Engine`. La migración reserva 00017; las llaves usan 00018. Una reversión de migración después de generar reenvíos puede fallar por unicidad antes que descartar historial: no es una herramienta de limpieza de datos.

El programador automático sigue en HU-gateway-21. Clerk real y publicación de staging se difieren por instrucción del responsable: por ahora se completa y prueba M2 localmente. Cloudflare queda elegido para la publicación futura; no se cambian DNS ni puertos del router en este trabajo local.
