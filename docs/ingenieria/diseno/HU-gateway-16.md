# HU-gateway-16 · Reintentos y deshabilitación de endpoints

## Política implementada

El ADR-0041 aceptado mantiene tres contadores independientes: cinco fallos para una fila de outbox, cinco intentos totales para una entrega y veinte fallos remotos consecutivos para un endpoint. El reparto `sent` no vuelve a fallar por un problema posterior de HTTP.

La base de HU-gateway-15 ya conserva entregas, arrendamientos e historial, con esperas de 30 segundos, 2 minutos, 10 minutos y 1 hora. Esta historia completa la actualización transaccional del contador del endpoint y su deshabilitación automática. Un 2xx pone el contador en cero; el vigésimo fallo deja `status=disabled` y el motivo fijo `20 consecutive delivery failures`. Ningún reclamo nuevo selecciona entregas de ese endpoint. Sus entregas e intentos permanecen consultables.

La finalización interna distingue explícitamente un resultado remoto observado de un fallo local. Un timeout del cliente HTTP cuenta como fallo remoto cuando el contexto de la pasada sigue activo; cancelar el proceso no se registra como resultado remoto. Un destino rechazado por validación DNS, incluso durante el dial, tampoco se atribuye al receptor. Un reclamo que vence consume un intento de la entrega y deja historial, pero no aumenta el contador remoto del endpoint.

`ListWebhookDeliveries` añade `failure_reason` con diagnósticos fijos, sin cuerpos de respuesta, URL ni credenciales. Conserva el estado propio de cada entrega y muestra el motivo de bloqueo si su endpoint está deshabilitado. No reabre trabajos ni reactiva endpoints. No hay migración nueva: los campos del contador y el motivo existen desde 00016.

Cada envío vuelve a calcular la firma sobre el cuerpo guardado y su marca de tiempo actual; las esperas no cambian `event.id` ni el cuerpo del trabajo.

## Pruebas

Se escribió primero la prueba de veinte fallos y se observó el fallo antes de implementar la nueva distinción de resultado. Pruebas con reloj controlado y PostgreSQL del proyecto comprueban:

- Cuatro eventos distintos, cinco intentos por entrega y veinte filas históricas; cada evento conserva su outbox `sent` con cero fallos de reparto.
- Ausencia de reclamo durante cada espera y después de agotar el presupuesto de cinco intentos.
- Deshabilitación exactamente al vigésimo fallo, con historial y motivo consultables.
- Diecinueve fallos seguidos de un 204 que reinicia el contador sin deshabilitar.
- Fallo local con un contador previo de diecinueve que lo conserva y mantiene el endpoint activo.
- Recuperación de arrendamientos vencidos y rechazo de resultados tardíos, conservados de HU-gateway-15.

Las cinco leyes siguen activas. La rotación de secretos y el reenvío manual siguen en HU-gateway-17; el ciclo automático en HU-gateway-21. Esta historia no cierra M2 ni sustituye la prueba de integración B3.

Validación: suite completa, cinco leyes, lint y detector de carreras de webhooks aprobados. La adición de `failure_reason` mantiene compatibilidad Protobuf frente a HU-gateway-15 y su generación es repetible. Se ejecutó PostgreSQL del proyecto en 55432 con Go 1.24.7.
