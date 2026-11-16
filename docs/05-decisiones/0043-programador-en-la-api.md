# ADR-0043 · Programador de timers y webhooks dentro de la API

- **Estado:** Aceptada
- **Fecha:** 2026-11-16
- **Responsables:** santy401
- **Aceptación:** santy401, 2026-11-16, confirmada en conversación.

## Contexto

HU-gateway-07 concreta S-P13, cuyo alcance ya aceptó el responsable: ejecutar timers y despacho en el binario de la API durante M2. El responsable aceptó también las cotas numéricas de este ciclo. M1 ofrece `Engine.FireDueTimers` y reclamos del outbox por espacio, pero ningún proceso los invoca periódicamente en producción.

El directorio interno y el reparto duradero se proponen en el [ADR-0041](0041-despacho-y-reintentos-de-webhooks.md). El ciclo no puede eludir RLS ni recibir el pool para descubrir espacios. Las operaciones propias del motor exigen un actor de sistema que nunca procede de una petición.

## Decisión

### Ubicación y límites

`services/api` arranca y detiene un único programador junto con el servidor HTTP. El programador recibe solo `Engine`, reloj y despachador; el despachador recibe también su cliente HTTP. Ninguno importa `kernel/state` ni recibe el pool. El actor `ActorSystem` se construye dentro del proceso y se limita a las operaciones internas fijas del motor.

M2 despliega una sola instancia activa de la API. No crea `kernel/coordination`, elección de líder ni `scheduler_runs`. Los bloqueos y reclamos de M1 siguen protegiendo cada operación, pero no sustituyen una política de coordinación para varias instancias. El despliegue debe detener la instancia anterior antes de activar el nuevo programador.

| Cota | Valor aceptado |
|---|---|
| Intervalo entre señales del ciclo | 1 segundo |
| Plazo máximo de una pasada | 30 segundos |
| Espacios seleccionados por pasada | Hasta 10 |
| Espacios procesados simultáneamente | Hasta 4, siempre distintos |
| Timers disparados por espacio y pasada | Hasta 100 |
| Filas de outbox `webhooks` repartidas por espacio y pasada | Hasta 100 |
| Entregas HTTP por espacio y pasada | Hasta 1 |
| Plazo de una llamada HTTP | 5 segundos, conforme al ADR-0041 |
| Espera máxima al apagar el programador | 10 segundos |

No se superponen pasadas. Las señales que llegan mientras se trabaja se agrupan; no generan una cola de ciclos pendientes. El plazo de cada operación es el tiempo restante de la pasada y los límites de HTTP no pueden prolongarlo. Las cotas se fijan como constantes comprobadas en pruebas; no se ofrece configuración arbitraria en M2.

### Equidad y transacciones

El motor enumera solo UUID de espacios activos mediante el directorio acotado del ADR-0041, con orden por UUID y cursor circular. El cursor del proceso avanza al iniciar el trabajo de cada espacio, incluso si este falla; al terminar el listado vuelve al comienzo. Se admiten hasta diez espacios por pasada y hasta cuatro en ejecución. La cancelación no adelanta el cursor sobre espacios que todavía no comenzaron. El reinicio pierde únicamente el cursor en memoria y vuelve al inicio; las colas duraderas no se pierden.

Por espacio se intenta primero `FireDueTimers`, después el reparto de outbox de webhooks y finalmente una entrega vencida. Cada etapa usa las operaciones fijas del motor y sus transacciones propias. Un error de una etapa se registra y permite intentar las siguientes mientras quede plazo, salvo cancelación o suspensión del espacio. No se mantiene una transacción abierta durante HTTP. El trabajo que supera las cotas espera otra vuelta del cursor; no se promete una latencia máxima de un segundo.

El estado activo se vuelve a verificar al operar dentro de cada espacio. Suspenderlo impide comenzar trabajo nuevo; no revoca una llamada HTTP ya iniciada. El suscriptor `internal` no se marca como enviado sin un consumidor implementado. n8n recibe webhooks conforme al ADR-0041.

### Errores, apagado y recuperación

Los errores de un espacio no detienen el servicio ni impiden atender otros espacios. Se registran identificadores de espacio, etapa y código de error, sin tokens, secretos ni cuerpos. Un error al enumerar espacios termina esa pasada y se vuelve a intentar en la señal siguiente; no genera un bucle ocupado.

Al recibir la señal de apagado, se detienen nuevas pasadas, se cancela el contexto de la actual y se espera hasta diez segundos. No se inventa un éxito ni se registra la cancelación local como fallo HTTP remoto. Un reclamo cuya finalización no pudo confirmarse se recupera mediante su arrendamiento. Puede repetirse un POST después de una caída; se mantiene el identificador estable del evento y la deduplicación corresponde al receptor.

Si el responsable rechaza esta propuesta y pospone el programador a M4, se conserva explícitamente la limitación: los compromisos vencidos no expiran automáticamente en staging y no hay despacho automático. HU-gateway-15 y 21 deben ajustar su ejecutor antes del código; no se anuncia cumplido el criterio completo de B3.

## Alternativas consideradas

| Alternativa | Motivo para descartarla en M2 |
|---|---|
| Servicio independiente y coordinación distribuida | Amplía el despliegue antes de requerir varias instancias |
| Procesar todos los espacios y trabajos sin límites | Permite que un espacio monopolice el proceso y retrase el apagado |
| Iniciar una pasada con cada tick sin esperar la anterior | Multiplica reclamos y llamadas concurrentes, e invalida el orden de fallos por endpoint |
| Recorrer espacios desde el gateway con acceso directo a la base | Elude el límite arquitectónico del motor |

## Consecuencias

- Timers y webhooks progresan sin un servicio adicional, con carga acotada y reparto entre espacios.
- La API y el trabajo periódico comparten proceso. Las cotas limitan la carga; el volumen real y las métricas podrán justificar otra decisión posterior.
- Se mantiene el límite de una instancia y no se agregan recordatorios previos al vencimiento ni reintentos de compromisos.
- El responsable aceptó esta decisión junto con ADR-0041 y 0042; HU-gateway-15 y 21 quedan habilitadas. No cambia el estado de aceptación de S-P13.

## Cumplimiento

Pruebas con reloj y despachador controlados para verificar agrupación de señales, ausencia de pasadas superpuestas, límites por espacio y globales, rotación con más de diez espacios, errores sin inanición, suspensión, avance del cursor y cancelación en cada etapa. Pruebas con PostgreSQL real comprueban aislamiento, vencimiento único y recuperación de reclamos. Una prueba de arquitectura impide importar `state` o exponer operaciones internas por una puerta. La prueba de cierre valida la cancelación sin esperas reales largas ni secretos en logs.

## Referencias

- [ADR-0031](0031-outbox-reclamo-y-arrendamiento.md), [ADR-0034](0034-vencimiento-de-compromisos-por-timer.md) y [ADR-0035](0035-motor-de-ejecucion.md).
- [ADR-0041](0041-despacho-y-reintentos-de-webhooks.md), [ADR-0042](0042-secretos-y-firma-de-webhooks.md), [kernel](../02-arquitectura/kernel.md) y [backlog](../product/backlog/m2-puertas-del-kernel.md).
