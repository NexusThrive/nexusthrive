# ADR-0031 · Outbox: reclamo de un solo espacio, arrendamiento, entrega al menos una vez y valores de partida

- **Estado:** Aceptada
- **Fecha:** 2026-11-14
- **Responsables:** santy401

## Contexto

Cada evento debe poder entregarse a uno o más suscriptores (`webhooks`, `n8n`, `ai`, `internal`) sin perderse y sin que dos reclamantes tomen la misma fila. HU-kernel-outbox-10 crea `kernel.outbox` y su reclamo, y cinco decisiones deben quedar escritas antes del código:

1. Un reclamo confirmado libera los bloqueos de fila: `FOR UPDATE SKIP LOCKED` solo protege mientras la transacción vive, y un consumidor trabaja después de confirmar. Hace falta una marca de reclamo que sobreviva a la transacción.
2. Un proceso que reclama y muere deja la fila reclamada para siempre si nada la libera.
3. El rol de la API no tiene `BYPASSRLS` (ADR-0027), así que una transacción solo ve un espacio.
4. Un mensaje que mata a su consumidor se reintentaría sin fin.
5. La ley de memoria cubre eventos y asientos; el outbox, en cambio, cambia de estado por diseño.

## Decisión

**Reclamo con arrendamiento.** `claim_token` (UUID v7 nuevo por llamada) y `claimed_until` (ahora más 5 minutos) marcan la fila reclamada. El reclamo toma filas `pending` o `failed` sin reclamo y con `next_attempt_at <= ahora`, en orden de `(next_attempt_at, event_seq)`, con `FOR UPDATE SKIP LOCKED` y `LIMIT`; no espera a las filas bloqueadas. Marcar una fila exige el token vigente: un token distinto o un reclamo ya vencido es `rule_violation`.

**Entrega al menos una vez.** Un consumidor que entrega y muere antes de marcar provoca una reentrega. El consumidor deduplica por `event.id`. No se promete entrega exactamente una vez.

**Un solo espacio por transacción.** `ClaimOutbox` reclama en el espacio que se le indica. El despacho entre espacios (lista de espacios activos, rotación y equidad) es de B3 y exigirá otro ADR; no se crea ningún rol con `BYPASSRLS` para evitarlo.

**Un reclamo vencido cuenta como intento fallido.** Antes de reclamar, la misma transacción vence los reclamos caducados (hasta 1000, con `SKIP LOCKED`): intento más uno, `last_error = 'claim expired'` y la espera de la tabla; al quinto intento la fila pasa a `dead`. Así un mensaje venenoso llega a `dead` en cinco vueltas.

**Valores de partida** que B3 puede cambiar con una migración: límite de 5 intentos (la fila pasa a `dead` en el quinto fallo, `attempts = 5`) y espera de `30 s, 2 min, 10 min, 1 h` tras los intentos 1 a 4. El límite vive en la restricción `outbox_state_check` y en `OutboxMaxAttempts`.

**El outbox es estado de trabajo, no memoria.** No es de solo agregar: su estado avanza por transiciones acotadas que un disparador impone a todos los roles, también al dueño: reclamar una fila libre, y de una reclamada solo enviar o fallar (`attempts` sube exactamente uno). `sent` y `dead` son terminales y la identidad de la fila (`event_seq`, `space_id`, `subscriber`, `created_at`) no cambia. No hay `DELETE` ni `TRUNCATE` (la retención llegará con su propia migración en B6). El evento sí sigue siendo de solo agregar. La base valida la forma de las transiciones, no el tiempo: no ve el reloj inyectable, y `delivered_at` y `next_attempt_at` los fija el kernel en Go.

**Escritura.** El outbox se escribe con `EnqueueOutbox` y `AppendEventAndEnqueue`, en la transacción del evento; `AppendEvent` no cambia. El permiso `kernel.outbox.claim` lo aplica HU-kernel-execution-13 y el reclamo no recibe actor en M1.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Solo `FOR UPDATE SKIP LOCKED` sin marca | El bloqueo termina al confirmar: dos reclamantes secuenciales obtendrían la misma fila. |
| Mantener la transacción abierta durante la entrega | Retiene una conexión y bloqueos durante una llamada de red. |
| Estado `claimed` en `status` | Mezcla el ciclo de vida con el reclamo y complica las restricciones; dos columnas lo separan. |
| Rol con `BYPASSRLS` para despachar todos los espacios | Rompe el aislamiento que ADR-0027 garantiza. |
| Entrega exactamente una vez | No existe sin cooperación del consumidor; se promete al menos una vez y deduplicación por `event.id`. |
| Reclamo vencido sin contar como intento | Un mensaje venenoso se reintentaría sin fin. |
| Hacer el outbox de solo agregar | Las transiciones de estado son su función. |

## Consecuencias

### Positivas

- Dos reclamantes nunca obtienen la misma fila, ni aunque el primero ya haya confirmado.
- Un proceso que muere no deja filas atascadas ni reintentos infinitos.
- Las transiciones inválidas (retroceder, saltar intentos, robar un reclamo) fallan en la base para todos los roles.

### Negativas

- La entrega puede repetirse: el consumidor debe deduplicar.
- Un espacio por transacción: el despachador de B3 debe conocer y rotar los espacios.
- Nada reabre una fila `dead` hasta la consola de B4.

### Riesgos

- Un superusuario puede deshabilitar los disparadores con DDL deliberado (límite de ADR-0028; auditoría de DDL en B6).
- El disparador no ve el reloj inyectable: el proceso del kernel que reclama puede forjar `delivered_at` o un reclamo anticipado. B3 no debe exponer estas funciones a clientes.
- Un espacio suspendido no se pausa en M1; lo decide HU-kernel-execution-13 o B4.
- Bajo `REPEATABLE READ` el reclamo daría `40001`; todo corre en `READ COMMITTED`.

## Cumplimiento

Pruebas de `kernel/db/outbox_schema_test.go` y `kernel/db/outbox_security_test.go` (grants por columnas, disparadores, `DELETE`, `TRUNCATE`, `COPY`, `ON CONFLICT`, clave foránea compuesta) y de `kernel/state/outbox_test.go` y `kernel/state/outbox_concurrency_test.go` (reclamo concurrente con 20 repeticiones, arrendamiento, límite de intentos, aislamiento entre espacios).

## Referencias

- [ADR-0027 · Roles de base de datos y RLS con SET LOCAL](0027-roles-y-rls.md)
- [ADR-0028 · Eventos de solo agregar](0028-orden-de-eventos.md)
- [ADR-0030 · Libro de doble partida](0030-libro-de-doble-partida.md)
- `docs/ingenieria/diseno/HU-kernel-outbox-10.md`
