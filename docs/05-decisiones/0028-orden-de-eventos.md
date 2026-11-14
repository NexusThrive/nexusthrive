# ADR-0028 · Eventos de solo agregar: inmutabilidad en dos capas y orden de `seq`

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

La ley de memoria dice que los hechos nunca se borran ni se reescriben. `kernel.events` es el registro de esos hechos y lleva un número de secuencia global, `seq`, que los consumidores (proyecciones, outbox, webhooks) usarán como cursor. Dos problemas exigen una decisión antes del código:

1. Quitar `UPDATE` y `DELETE` al rol de la API no basta. El dueño de la tabla y el administrador los tienen por defecto, y una migración futura puede conceder un permiso por error.
2. `seq` sale de una secuencia, que se asigna al insertar y no al confirmar. Una transacción con `seq` menor puede confirmarse después de otra con `seq` mayor. Un lector que guarde el cursor `seq >= n` saltaría ese evento para siempre si ya vio el mayor.

## Decisión

**Inmutabilidad en dos capas.**

- Capa de permisos: el rol de la API recibe `SELECT` sobre la tabla e `INSERT` por columnas, sin la columna `seq`, y la RLS tiene solo políticas `SELECT` e `INSERT`. Un `UPDATE`, `DELETE` o `TRUNCATE` de la API falla con `42501`. El `GRANT INSERT` por columnas hay que actualizarlo a mano cuando se añade una columna a `kernel.events`; la prueba con `column_privileges` lo detecta.
- Capa de disparadores: un disparador `BEFORE UPDATE OR DELETE` por fila y otro `BEFORE TRUNCATE` por sentencia rechazan el cambio con `restrict_violation`, incluso para el dueño y el administrador, y aunque un permiso se conceda por error. `seq` es `GENERATED ALWAYS`, así que un `INSERT` ordinario no puede fijarlo, y la falta de `INSERT` sobre esa columna impide también `OVERRIDING SYSTEM VALUE`. Un `INSERT` de tabla completa lo permitiría y un `seq` adelantado a la secuencia haría chocar con la clave primaria los siguientes eventos de todos los espacios.
- Límite aceptado: un superusuario puede deshabilitar el disparador (`ALTER TABLE ... DISABLE TRIGGER`) o usar `session_replication_role = replica`. Es DDL deliberado y queda en el registro del servidor, no una escritura accidental. B6 puede añadir auditoría de DDL.

**Garantías de `seq`.** El kernel garantiza que:

1. `seq` es único y crece por orden de asignación en todo el sistema.
2. Se permiten huecos (un rollback consume `seq`), nunca repetidos.
3. Cada lectura devuelve solo eventos confirmados, en orden ascendente de `seq`.

El kernel no garantiza que lecturas sucesivas no revelen después un `seq` menor al ya visto. "Desde `seq = n`" es inclusivo (`>=`) y el cursor del consumidor es el último `seq` visto más uno.

**Deber del consumidor.** Todo consumidor que lea por cursor debe reanudar con un traslape de ventana y deduplicar por `id`, o exigir un horizonte estable. La opción robusta (una columna `xid8 DEFAULT pg_current_xact_id()` y un filtro con `pg_snapshot_xmin`) se decide en B3 con su propio ADR.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Solo permisos, sin disparador | No protege del dueño ni de un permiso concedido por error. |
| Serializar escritores con un bloqueo asesor | Cuello de botella global y sin garantía estricta de orden de confirmación. |
| Columna `xid8` y filtro de horizonte ya en M1 | Ningún consumidor por cursor existe todavía; se decide en B3 con datos reales. |
| Reglas (`CREATE RULE`) que ignoran el cambio | Fallan en silencio: un `UPDATE` parecería exitoso. |

## Consecuencias

### Positivas

- La memoria no se reescribe ni por accidente del dueño.
- El contrato de orden queda escrito antes de que existan consumidores.

### Negativas

- Un consumidor ingenuo por cursor puede perder un evento; la responsabilidad queda documentada.
- Corregir un dato exige un evento compensatorio, nunca una edición.

### Riesgos

- Un superusuario deliberado puede saltarse el disparador.
- El índice `(space_id, seq)` crece con la tabla; el particionado queda para B6.
- El outbox (HU-kernel-outbox-10) no sufre el problema porque se reclama por estado y `next_attempt_at`, no por cursor de `seq`.

## Cumplimiento

Pruebas contra PostgreSQL real: `UPDATE`, `DELETE` y `TRUNCATE` como rol de la API (42501) y como administrador (`restrict_violation`), el disparador con permisos concedidos por error, `seq` no fijable, unicidad bajo concurrencia con rollbacks y la prueba que documenta que `seq` es orden de asignación y no de confirmación.

## Referencias

- `docs/02-arquitectura/modelo-de-datos/kernel.md`, sección 4 y Reglas
- `docs/01-modelo/leyes.md`, ley de memoria
- ADR-0009 (libro de movimientos), ADR-0027 (roles y RLS)
- `docs/ingenieria/diseno/HU-kernel-events-09.md`
