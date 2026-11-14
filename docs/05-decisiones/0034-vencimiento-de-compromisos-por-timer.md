# ADR-0034 · Vencimiento de compromisos por timer: `expired` solo por cascada desde un timer disparado, con el reloj de la base como techo

- **Estado:** Aceptada
- **Fecha:** 2026-11-14
- **Responsables:** santy401

## Contexto

HU-kernel-timers-12 cierra la ley de movimiento (`docs/01-modelo/leyes.md`): un compromiso que no se cumple a tiempo vence y el sistema lo registra. El ADR-0033 dejó el estado `expired` como gancho y el seguimiento 53 pendiente: el rol de la API no puede escribir `status`, pero el disparador de `commitments` no ve el reloj inyectable (seguimientos 41 y 45) y por eso no puede comprobar `due_at <= reloj`. Antes de escribir código (regla 12 de `AGENTS.md`) hay que decidir cómo se fija `expired`, qué condiciones lo permiten y qué garantiza la base cuando no hay disparadores deshabilitados. Siguen vigentes el ADR-0027 (roles y RLS), el ADR-0028 (inmutabilidad en dos capas), el ADR-0031 (outbox) y el ADR-0033 (compromisos).

## Decisión

**Un timer por compromiso, creado por la base.** Un disparador `AFTER INSERT` en `commitments` crea el timer de vencimiento (`kernel.timers`) con el mismo `id` del compromiso y `fire_at = due_at`; otro lo reprograma cuando cambia `due_at` y lo cancela cuando el compromiso se cierra. Valen para todo escritor (rol de la API, administrador y futuras operaciones) y no hay cómo olvidarlos. `due_at` deja de ser inmutable (relaja el ADR-0033): se cambia con `UPDATE (due_at)`, solo mientras el compromiso está abierto y nunca hacia el pasado, porque si no quien tenga el rol de la API forzaría el vencimiento de cualquier compromiso. Un `due_at` pasado se admite al crear y vence en el primer disparo.

**`expired` se fija solo por cascada.** El disparador de `UPDATE` de `commitments` admite `status = 'expired'` únicamente cuando se cumplen las tres condiciones a la vez:

1. `OLD.due_at <= now()`: el vencimiento ya pasó según el reloj de la base (la hora de la transacción), no según un valor del llamador.
2. Existe un timer del compromiso con `status = 'fired'` y `fired_at = now()`: se disparó en esta misma transacción, no en otra.
3. Ese timer tiene `fire_at <= now()`.

El timer, a su vez, solo pasa de `scheduled` a `fired` si el compromiso está abierto y `OLD.fire_at <= now()`; el disparador fija `fired_at := now()` y rechaza cualquier valor del llamador. La cascada (`AFTER UPDATE` del timer) lleva el compromiso a `expired` con los derechos de quien escribe, sin `SECURITY DEFINER`: por eso el rol de la API recibe `UPDATE (due_at, status)` sobre `commitments`, ambas columnas guardadas. Cerrar el compromiso por cumplimiento o cancelación cancela el timer; cancelarlo con el compromiso abierto se rechaza.

**El reloj inyectable es solo un techo por detrás.** `FireDueTimers` consulta `fire_at <= LEAST(reloj, now())`: un reloj atrasado dispara menos, uno adelantado no dispara de más. Las pruebas crean compromisos con `starts_at` y `due_at` en el pasado (`starts_at` es insertable). Dispararlos es una sola operación (reclamar y disparar en una transacción, sin arrendamiento ni `claim_token`), con orden de bloqueo compromiso y luego timer en todo el kernel y `FOR NO KEY UPDATE OF c SKIP LOCKED`, de modo que el doble disparo concurrente deja un solo disparo y un solo evento, y un compromiso ocupado se atiende en la ronda siguiente.

**El evento es obligatorio y la coherencia se verifica al confirmar.** Cada disparo escribe, en la misma transacción, el evento `kernel.commitment.expired` v1 (`subject_ref = 'kernel.commitment/<id>'`, actor `system`) y su fila de outbox para el suscriptor `internal`. Una restricción diferida (`CONSTRAINT TRIGGER ... DEFERRABLE INITIALLY DEFERRED`, `timers_coherent`) sobre `timers` y `commitments` comprueba al confirmar, también para el administrador: un compromiso abierto tiene exactamente un timer `scheduled` con `fire_at = due_at`; uno cerrado no tiene ninguno; un timer `fired` implica compromiso `expired` y un evento del mismo espacio con ese tipo, versión 1 y asunto. Lee con RLS, de modo que cambiar el espacio antes de confirmar falla cerrado. Es la ley de memoria aplicada a todo escritor.

**Timers inmutables en su historia.** Un timer terminal (`fired` o `canceled`) no cambia; no se borra ni se trunca (disparadores de fila y de `TRUNCATE`, también en cascada), con las dos capas del ADR-0028. Se omiten `signal_type` y `payload` del modelo de datos (regla 11: los recordatorios previos quedan fuera de alcance).

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Aceptar el reloj inyectado sin techo | Quien tenga el rol de la API podría expirar cualquier compromiso mintiendo con el reloj. |
| Conceder `UPDATE (status)` sin las tres condiciones | Permitiría marcar como vencida cualquier deuda; la guarda debe poder probar el vencimiento por sí sola. |
| Crear el timer desde Go (`AppendCommitment`) | No vale para SQL directo ni para operaciones futuras; es fácil de olvidar. |
| `SECURITY DEFINER` para la cascada | Amplía privilegios; basta con guardar las dos columnas. |
| Arrendamiento (`claim_token`) para los timers | Reclamar y disparar son una sola transacción; el bloqueo de fila y `SKIP LOCKED` bastan. |
| Dejar `due_at` inmutable | Impide reprogramar, que la historia exige (criterio 2). |

## Consecuencias

### Positivas

- Un compromiso solo vence con la hora de la base y un timer realmente vencido, y la memoria del hecho (evento y outbox) llega siempre con él.
- El doble disparo concurrente es imposible por construcción; cumplir, cancelar, reprogramar y disparar en paralelo tienen un solo ganador.
- Se resuelve el seguimiento 53.

### Negativas

- La API recibe `UPDATE (due_at, status)` sobre `commitments` y `UPDATE (status, fire_at)` sobre `timers`, concedidos solo porque los disparadores corren con sus derechos.
- Reprogramar no escribe evento (seguimiento 60).
- El evento solo llega a `internal` (seguimiento 64).

### Riesgos

- Quien tenga el rol de la API puede disparar por SQL timers realmente vencidos: es el proceso del kernel (seguimientos 42 y 45); el evento obligatorio al confirmar cierra el hueco de memoria.
- Un administrador puede deshabilitar disparadores (límite del ADR-0028); quedan `CHECK`, `UNIQUE`, claves foráneas compuestas e índice parcial.
- Un escritor directo sobre `timers` puede dar `40P01`; el llamador reintenta. Sin garantía de latencia bajo `SKIP LOCKED` (seguimiento 61).
- Hasta que exista el proceso programador, nada dispara timers en producción.

## Cumplimiento

Pruebas de `kernel/db` (esquema, grants, disparadores, ataques con el rol de la API y el administrador, restricción diferida), `kernel/model`, `kernel/state` (incluidas carreras reales) y `kernel/capability`, sin `t.Skip`. La prueba de arquitectura vigila el vocabulario del kernel y `adr_test.go` valida el formato.

## Referencias

- `docs/ingenieria/diseno/HU-kernel-timers-12.md`
- ADR-0027, ADR-0028, ADR-0031, ADR-0033
- `docs/01-modelo/leyes.md`, `docs/02-arquitectura/modelo-de-datos/kernel.md`
