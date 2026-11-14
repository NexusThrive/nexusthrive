# ADR-0033 · Compromisos: estado derivado, concurrencia por bloqueo de fila, monto cumplido verificado y cumplimientos inmutables

- **Estado:** Aceptada
- **Fecha:** 2026-11-14
- **Responsables:** santy401

## Contexto

HU-kernel-commitments-11 implementa la ley de compromiso (`docs/01-modelo/leyes.md`): una obligación tiene deudor, acreedor, recurso, monto y vencimiento, y se cierra cumpliéndose o cancelándose con motivo. Antes de escribir código (regla 12 de `AGENTS.md`) hay que decidir quién fija el estado y el monto cumplido, cómo se evita que dos cumplimientos concurrentes superen el monto, qué asiento puede cumplir un compromiso y qué ocurre con el historial de cumplimientos. El criterio 2 de la historia exige `status` sin valor por defecto y el criterio 4 habla de un `CHECK`; ambos se concilian con la derivación por disparador. Las reglas de ADR-0027 (roles y RLS), ADR-0028 (inmutabilidad en dos capas) y ADR-0030 (libro de doble partida) siguen vigentes.

## Decisión

**Estado y cierre derivados.** El rol de la API inserta un compromiso solo con estado `open`; un disparador rechaza cualquier otro valor y también `fulfilled_amount`, `closed_at` o `canceled_reason` falsos. Después `status` no se actualiza por la API (no tiene `UPDATE` sobre esa columna): el kernel lo deriva. Un compromiso pasa a `fulfilled` cuando el último cumplimiento completa el monto y a `canceled` cuando se escribe un motivo; `closed_at` lo fija el kernel con el reloj de la base. Los estados terminales (`fulfilled`, `canceled`, `expired`) no se reabren ni se editan, tampoco por el dueño de las tablas. `expired` queda en el modelo de cuatro estados como gancho de timers-12; `leyes.md` cita tres y se sigue el modelo de datos.

**Concurrencia por bloqueo de fila.** Cumplir toma `FOR NO KEY UPDATE` sobre el compromiso dentro del disparador de `fulfillments`, antes de leer el pendiente. La segunda transacción espera, relee la versión confirmada y rechaza con `rule_violation` si el cumplimiento excede lo pendiente. No se usa un `CHECK` (no ve filas ajenas) ni un saldo calculado en cada lectura.

**`fulfilled_amount` derivado y verificado.** La columna existe para que consultar el avance sea barato. El disparador de `UPDATE` solo admite el valor igual a la suma de `fulfillments` del compromiso, y una comprobación diferida al confirmar lo vuelve a verificar. El rol de la API necesita `UPDATE (fulfilled_amount)` porque los disparadores corren con los derechos de quien escribe (sin `SECURITY DEFINER`), pero el único valor aceptable es el verdadero. El `CHECK` de que no supere `amount` es la última capa y se prueba con el disparador deshabilitado en una base desechable.

**Un asiento cumple un solo compromiso, una sola vez y completo.** El cumplimiento enlaza un asiento del libro con un compromiso (`UNIQUE (entry_id)`, `amount` igual al del asiento). El asiento debe ser positivo, de una cuenta cuyo titular es el acreedor, del mismo recurso y espacio, y no de un intercambio de compensación. Las claves foráneas compuestas incluyen espacio y recurso, de modo que mezclar espacios o recursos falla aunque se borre un disparador.

**Cumplimientos inmutables.** `fulfillments` es de solo agregar con las dos capas de ADR-0028 (sin privilegios y con disparadores de fila y de `TRUNCATE`, también en cascada). `compromisos` no se borra. Cambiar `scale` o `kind` de un recurso con compromisos se rechaza.

**`entries.commitment_id` se descarta.** `fulfillments` es la única fuente de verdad del vínculo; `entries` es inmutable y una columna extra sería forjable con su grant. Se actualizan los documentos que la prometían.

**Enmienda (2026-11-14, ADR-0034).** El rol de la API recibe `UPDATE (due_at, status)` sobre `commitments` porque la cascada de un timer corre con sus derechos, y `due_at` deja de ser inmutable: se reprograma solo hacia el futuro. `expired` se fija únicamente por cascada desde un timer disparado en la misma transacción, con `due_at <= now()`; resuelve el seguimiento 53.

**Alcance.** Sin timers, vencimiento, eventos ni outbox (timers-12 y execution-13); los permisos `kernel.commitment.*` los aplica execution-13.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Que la API fije `status` y `fulfilled_amount` | Permitiría marcar como cobrada cualquier deuda sin cumplimientos. |
| `CHECK` o saldo calculado para limitar el monto | Un `CHECK` no ve filas de otras transacciones; el saldo calculado encarece cada lectura. |
| Candados consultivos y cumplimiento parcial por asiento | Añade carreras y la historia no lo exige (seguimiento 52). |
| `commitment_id` en `entries` | Duplica la fuente de verdad en una tabla inmutable y forjable. |
| `SECURITY DEFINER` para mantener `fulfilled_amount` | Amplía privilegios; basta con validar el valor contra la suma. |

## Consecuencias

### Positivas

- El estado y el avance no se pueden falsificar con el rol de la API ni con el dueño: lo impone la base.
- Dos cumplimientos concurrentes nunca superan el monto; hay un ganador y un `rule_violation`.
- El historial de cumplimientos es de solo agregar.

### Negativas

- `fulfilled_amount` lleva un `UPDATE` concedido que solo acepta el valor derivado.
- Un compromiso cumplido no se reabre si se compensa el intercambio del asiento (seguimiento 51).

### Riesgos

- Un superusuario o dueño puede deshabilitar disparadores (límite de ADR-0028); `CHECK`, `UNIQUE` y claves foráneas siguen.
- Cumplir dos compromisos en orden opuesto desde transacciones distintas puede dar `40P01`; el llamador reintenta.
- timers-12 debe acotar la vía de `expired` (seguimiento 53).

## Cumplimiento

Pruebas de `kernel/db` (esquema, grants, disparadores, ataques con el rol de la API y el administrador), `kernel/model` y `kernel/state` (incluida una carrera real), sin `t.Skip`. La prueba de arquitectura vigila el vocabulario del kernel.

## Referencias

- `docs/ingenieria/diseno/HU-kernel-commitments-11.md`
- ADR-0027, ADR-0028, ADR-0030
- `docs/01-modelo/leyes.md`, `docs/02-arquitectura/modelo-de-datos/kernel.md`
