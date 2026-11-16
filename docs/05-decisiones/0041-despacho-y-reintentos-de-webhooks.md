# ADR-0041 · Despacho duradero y reintentos de webhooks

- **Estado:** Propuesta
- **Fecha:** 2026-11-16
- **Responsables:** santy401

## Contexto

HU-gateway-06 debe decidir el despacho antes de HU-gateway-15 y 16. El outbox tiene una fila por evento y suscriptor, no por endpoint. Un evento puede interesar a varios endpoints del mismo espacio: repetir toda la distribución después de un fallo no debe duplicar sus trabajos ni perder destinatarios.

El ADR-0031 fija cinco fallos del outbox; `puertas.md` fija veinte fallos consecutivos de un endpoint; el flujo de tareas fallidas de plataforma menciona ocho reintentos sin distinguirlos. Son contadores diferentes. El archivo de flujos existente es `docs/02-arquitectura/plataforma/flujos.md`; la ruta sin `plataforma/` citada en el backlog original no existe.

## Decisión propuesta

### Publicación y entrega

El proceso corre dentro del binario de la API, conforme a S-P13. El despachador recibe solo `Engine`, reloj y cliente HTTP; no recibe pool ni importa `state`. El proceso crea su actor `system`, nunca desde una petición. El ciclo y sus intervalos pertenecen a HU-gateway-07.

La operación que produce un evento público escribe su fila para `webhooks` en la misma transacción del evento, además de los suscriptores internos que ya correspondan. `kernel.exchange.recorded` y `kernel.commitment.expired` v1 llegan a `internal` y `webhooks`. n8n recibe los eventos mediante un endpoint de webhook; no se crean filas `n8n` o `ai` sin un consumidor implementado. Las pruebas existentes se actualizan para verificar los dos suscriptores, sin omitir ninguna ley.

El worker reclama una fila `webhooks` por la ruta interna del motor y materializa trabajos para los endpoints activos que coinciden con el tipo y la versión. La selección y los trabajos se confirman junto con la marca `sent` del outbox, comprobando el token de reclamo. Esa marca significa que el suscriptor de webhooks aceptó duraderamente el trabajo; no certifica que todos los receptores HTTP lo hayan confirmado. Un evento sin destinatarios también se confirma como distribuido.

La unicidad de la entrega original por `(space_id, endpoint_id, event_seq)` evita duplicar el reparto si el proceso cae. `webhook_deliveries` mantiene el trabajo y su estado; `webhook_delivery_attempts` conserva los intentos como filas nuevas. Un reenvío manual crea otro trabajo con `replay_of`, manteniendo intacto el original. Los FK de endpoint, evento, entrega e intento incluyen `space_id` y todas las tablas tienen RLS por espacio.

El worker reclama después cada entrega, confirma el reclamo, hace el POST sin una transacción de base abierta y registra su resultado por el motor. El arrendamiento es de cinco minutos y la llamada HTTP dura como máximo cinco segundos. Una caída después del POST puede causar una repetición: se conserva `event.id` y el receptor deduplica por ese identificador. No se promete entrega exactamente una vez.

En M2 se procesa como máximo una entrega HTTP por espacio en cada pasada, sin superponer pasadas del mismo espacio. Así no hay dos intentos simultáneos de un endpoint dentro de la única instancia y el contador consecutivo tiene un orden definido. Cada resultado, incluido un reclamo vencido, agrega un intento histórico y actualiza el estado agregado en la misma transacción; no sobrescribe intentos anteriores.

### Tres límites diferentes

| Contador | Límite | Efecto |
|---|---|---|
| Fallos al procesar la fila de outbox | 5 fallos, como ADR-0031 | Outbox `dead`; no se vuelve a reclamar |
| Intentos de una entrega a un endpoint | 5 intentos totales, incluido el primero | Entrega `dead`; requiere reenvío explícito |
| Fallos HTTP consecutivos observados de un endpoint | 20 fallos, acumulados entre entregas | Endpoint `disabled`, con motivo |

Outbox y entregas usan esperas de 30 segundos, 2 minutos, 10 minutos y 1 hora después de los fallos 1 a 4. No hay sexto intento automático. El fallo de una descarga HTTP afecta a su entrega y al endpoint, no vuelve a fallar una fila de outbox cuyo reparto ya se confirmó. Un éxito 2xx pone en cero el contador consecutivo. Un código no 2xx o timeout HTTP es un fallo. Un reclamo vencido consume un intento del trabajo, pero no atribuye un fallo al endpoint sin una respuesta HTTP observada. Cancelar el proceso no se trata como un fallo remoto observado.

Una entrega a un endpoint deshabilitado no se envía; su estado conserva el motivo y no aumenta el contador remoto. Deshabilitarlo no borra entregas ni eventos. Reabrir filas del outbox `dead` sigue siendo trabajo de B4; no se implementa una vía pública de reclamo o de marcado.

### Selección de espacios y seguridad de red

El motor ofrece un listado interno paginado de UUID de espacios activos para el worker, ordenado por UUID y con cursor circular. Una función fija de solo lectura, sin parámetros de SQL, filtra `status=active` y limita el tamaño. Se añade mediante migración y una excepción de directorio explícita con rol NOLOGIN sin BYPASSRLS; no se amplía la lectura normal de la API ni se expone una ruta para listar espacios ajenos. La implementación conserva las pruebas que permiten únicamente las firmas privilegiadas aprobadas.

El worker rota entre espacios y limita el trabajo por espacio. Reclamar y marcar ocurren siempre en una transacción del espacio correspondiente y vuelven a verificar su estado: suspenderlo pausa el trabajo nuevo. Las cotas del ciclo se fijan en el ADR del programador.

Los destinos requieren HTTPS fuera de `local`. Se rechazan credenciales en la URL, loopback, direcciones privadas, locales de enlace, indeterminadas y multicast. Se valida el DNS al registrar y en cada conexión, fijando una dirección pública para el dial y conservando el hostname para TLS. No se siguen redirecciones. El modo local permite un receptor de prueba explícito; ese permiso no puede activarse en staging o producción.

El cuerpo queda fijado al materializar el trabajo: identificador estable, tipo, versión, secuencia como cadena, fecha, actor del evento, identificador de solicitud y payload del espacio. Nunca incluye credenciales ni secretos. Los filtros de endpoint seleccionan el nombre público versionado, por ejemplo `kernel.exchange.recorded.v1`, derivado de tipo y versión, para no habilitar una versión incompatible automáticamente.

### Operaciones públicas

Se agregan los permisos `kernel.webhook.read` (`read`) y `kernel.webhook.write` (`write`) al catálogo y al contrato. Crear endpoint, rotar secreto y reenviar requieren escritura; consultar entregas requiere lectura. No exigen una capacidad adicional. La creación y rotación no guardan su respuesta en el registro genérico de idempotencia, porque contienen un secreto de una sola presentación; el reenvío sí puede ser idempotente y responde solo con el identificador del trabajo.

Los permisos se conceden explícitamente en los espacios existentes; no se editan silenciosamente roles ya guardados. El rol de propietario generado para espacios nuevos incorpora estas claves. `ClaimOutbox` y los marcadores siguen siendo operaciones internas.

## Alternativas consideradas

| Alternativa | Motivo para descartarla |
|---|---|
| Hacer todos los POST antes de confirmar el outbox | Un fallo parcial vuelve a recorrer todos los destinatarios y mezcla reparto con entrega |
| Considerar todos los fallos como el mismo contador | Confunde el mensaje fallido, el trabajo de un endpoint y la salud del receptor |
| Mantener la transacción abierta durante HTTP | Retiene conexiones y bloqueos mientras responde un servicio externo |
| Listar todos los espacios con el rol normal de la API | Debilita el aislamiento para resolver una necesidad interna |

## Consecuencias

- El reparto es duradero y cada endpoint tiene reintentos e historial independientes.
- Se agrega una tabla de intentos y una función interna para el worker. La transición `sent` del outbox debe interpretarse como aceptación duradera del reparto, no como recibo HTTP final.
- Se conservan las cinco reglas del outbox del ADR-0031 y se reemplaza la cifra genérica de ocho por límites explícitos para M2.
- Estas son decisiones nuevas, pendientes de aceptación. No se implementan tablas, permisos ni despacho hasta aceptarlas junto con el ADR de secretos y el del programador.

## Cumplimiento

Pruebas de reparto repetido sin duplicados, recuperación tras caída en cada frontera, aislamiento, destinatarios versionados, quinto fallo de outbox y entrega, vigésimo fallo del endpoint, éxito que reinicia el contador, cancelación local sin falsa deshabilitación, rotación de espacios, DNS privado o cambiante, redirecciones y ausencia de rutas públicas internas. El reloj y los receptores son inyectables; ninguna prueba requiere cuentas ni red externa.

## Referencias

- [ADR-0031](0031-outbox-reclamo-y-arrendamiento.md), [ADR-0038](0038-resolucion-de-actor-y-espacio.md) y [ADR-0042 (propuesta)](0042-secretos-y-firma-de-webhooks.md).
- [Modelo de datos](../02-arquitectura/modelo-de-datos/kernel.md), [flujos de plataforma](../02-arquitectura/plataforma/flujos.md), [puertas](../04-integraciones/puertas.md) y [backlog](../product/backlog/m2-puertas-del-kernel.md).
