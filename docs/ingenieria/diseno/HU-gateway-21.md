# HU-gateway-21 · Programador dentro de la API

## Comportamiento

Implementa ADR-0043 aceptado. La API construye un solo motor y comparte el despachador configurado entre las rutas de registro y el proceso periódico. El programador recibe únicamente `Engine`, el despachador HTTP y el logger; no recibe un pool ni importa `state`.

Las señales llegan cada segundo. Una pasada dura como máximo treinta segundos, enumera hasta diez espacios activos y mantiene hasta cuatro trabajadores simultáneos en espacios diferentes. No se superponen pasadas: una señal concurrente se agrupa. El cursor circular avanza cuando se inicia cada intento, incluso si falla; no salta espacios que todavía esperan capacidad al cancelarse la pasada.

Por espacio se intenta primero disparar hasta cien timers, después repartir hasta cien filas de outbox y finalmente una entrega HTTP. Cada etapa usa su transacción y comprueba permisos y estado del espacio. Un error de una etapa no impide las siguientes salvo suspensión o cancelación. HTTP ocurre fuera de la transacción y conserva su timeout de cinco segundos. Sin configuración de webhooks, los timers siguen activos y las filas de webhooks permanecen pendientes.

El actor `system` y el identificador de solicitud nacen dentro del proceso. Los logs identifican espacio, etapa y código fijo; no incluyen errores crudos, payloads, tokens o secretos. El arranque declara explícitamente el límite de una instancia activa. No se agregan `kernel/coordination`, elección de líder ni `scheduler_runs`.

Al terminar el servidor o recibir una señal, se cancela el contexto, se dejan de iniciar pasadas, se apaga HTTP y se espera al trabajador con un presupuesto conjunto de diez segundos. Los reclamos no confirmados se recuperan por arrendamiento; cancelar no cuenta como fallo remoto.

## Pruebas

Pruebas escritas primero y ejecutadas con detector de carreras:

- Capacidad máxima de cuatro trabajadores, diez espacios por pasada, cursor circular sobre doce espacios y ausencia de pasadas superpuestas.
- Cancelación con una señal de ciclo pendiente y sin comenzar otra pasada al apagar.
- Continuación tras errores de timers o reparto, pausa del espacio suspendido y ausencia de datos sensibles en logs.
- PostgreSQL real: un compromiso vencido llega a `expired` una sola vez y produce un evento y las dos filas de outbox, aun sin configuración de webhooks.
- Ciclo de vida de la API: no retorna antes de detener al programador.

Durante la revisión del editor se corrigieron tres helpers de pruebas de `execution` que no comprobaban `rows.Err()`. El diagnóstico de `gopls` sobre todos los archivos de `execution` y `gateway` quedó limpio; no era un fallo de compilación de esas carpetas.

## Límite de despliegue

Las pruebas de M2 se completan localmente por instrucción del responsable. Render se está evaluando como alojamiento futuro, pero sus despliegues pueden superponer procesos: antes de usarlo hay que resolver expresamente el requisito de un solo programador activo. Esta historia no cambia la decisión a coordinación multiinstancia ni modifica servicios externos.
