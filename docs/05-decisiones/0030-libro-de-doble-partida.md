# ADR-0030 · Libro de doble partida: conservación diferida, inmutabilidad en dos capas, compensación y decimales

- **Estado:** Aceptada
- **Fecha:** 2026-11-14
- **Responsables:** santy401

## Contexto

La ley de conservación dice que el valor no se crea ni se destruye: cada intercambio mueve recursos entre cuentas y sus asientos suman cero por recurso. La ley de memoria dice que lo ocurrido no se reescribe. HU-kernel-exchanges-08 crea `kernel.exchanges` y `kernel.entries`, el punto más delicado de M1, y cuatro decisiones deben quedar escritas antes del código:

1. Un intercambio se escribe fila por fila, así que la suma cero solo puede comprobarse al final de la transacción, no en cada `INSERT`.
2. Quitar privilegios al rol de la API no protege del dueño, del administrador ni de una migración que conceda un permiso por error (ADR-0028).
3. Un error se corrige con otro intercambio, y hay que definir qué es corregir bien: una sola vez y con los asientos exactamente opuestos.
4. El dinero no puede pasar por `float`, y el kernel necesita un decimal exacto en Go sin dependencias nuevas.

## Decisión

**Conservación diferida.** Disparadores de restricción `DEFERRABLE INITIALLY DEFERRED` sobre `exchanges` y `entries` comprueban al confirmar, sobre `kernel.entries`: (i) al menos dos asientos, (ii) `sum(amount) = 0` por `(exchange_id, resource_id)` y (iii) si el intercambio compensa a otro, suma neta por cuenta igual al opuesto de la del original. El disparador sobre `exchanges` es el que rechaza el intercambio vacío, porque sobre `entries` nunca se dispararía sin filas. La operación de Go adelanta la comprobación con `SET CONSTRAINTS ... IMMEDIATE` para devolver un error tipado, pero la garantía final es el disparador diferido al confirmar. Los montos cero, `NaN` e infinitos se rechazan con `CHECK`.

**Inmutabilidad en dos capas** (como ADR-0028). Capa de permisos: el rol de la API solo tiene `SELECT` e `INSERT` por columnas (sin `recorded_at`), sin `UPDATE`, `DELETE`, `TRUNCATE` ni `REFERENCES`, y la RLS solo tiene políticas `SELECT` e `INSERT`. Capa de disparadores: `BEFORE UPDATE OR DELETE` por fila y `BEFORE TRUNCATE` por sentencia, en ambas tablas, fallan con `restrict_violation` incluso para el dueño, el administrador y `TRUNCATE ... CASCADE` desde tablas padre. `recorded_at` lo fija un disparador con `now()`: nadie lo falsifica. Un asiento solo puede añadirse a un intercambio creado en la misma transacción (`xmin`), de modo que la historia confirmada no admite asientos nuevos, aunque sean balanceados. Un asiento sobre una cuenta se admite solo si la cuenta está abierta, lee su estado con `FOR SHARE` (se serializa con el cierre) y respeta el recurso y la escala de ese recurso. Ninguna función es `SECURITY DEFINER`; todas fijan `search_path` y califican sus objetos.

**Compensación.** Un intercambio se corrige con otro cuyo `reverses_exchange_id` apunta al original, con `kind = kernel.exchange.reversal`. Un índice único `(space_id, reverses_exchange_id)` permite compensar una sola vez y decide las carreras. "Invertir" significa que la suma neta por cuenta del compensatorio es el opuesto exacto de la del original, en las mismas cuentas; una cuenta cerrada después del original impide compensarlo hasta que una ADR defina una excepción.

**Decimales.** En PostgreSQL, `numeric` con `CHECK` de rango (valor absoluto menor que 1e30); la escala se valida por recurso y el valor se guarda canónico (`round(amount, scale)`). En Go, `model.Amount` es un decimal exacto sobre `math/big` (hasta 30 dígitos enteros y 18 decimales, sin exponente, `NaN` ni signo `+`), sin dependencia nueva y sin `float`.

**Identificación del intercambio.** `idempotency_key` es obligatoria y única por espacio (las operaciones de dinero siempre son idempotentes). La clave repetida se rechaza como segunda barrera; la idempotencia de las operaciones la resuelve el motor (ver la enmienda de 2026-11-14). El actor es un participante del mismo espacio, siempre presente.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Comprobar la suma en cada `INSERT` de asiento | Imposible: el primer asiento nunca suma cero. |
| Una sola fila de intercambio con arreglo de asientos | Pierde claves foráneas, índices por cuenta y las comprobaciones por fila. |
| Solo permisos, sin disparadores de memoria | No protege del dueño ni de un permiso concedido por error. |
| Reglas (`CREATE RULE`) que ignoran el cambio | Fallan en silencio. |
| Función `SECURITY DEFINER` para escribir el libro | Amplía la superficie de ataque y el rol de la API no la necesita. |
| `float` o `big.Float` para los montos | Pierden exactitud; `big.Float` arrastra precisión binaria. |
| Biblioteca de decimales externa | Dependencia nueva sin necesidad: se necesita suma, signo y comparación. |
| Permitir asientos nuevos en un intercambio confirmado si balancean | Reescribe la historia y abre una carrera entre transacciones. |

## Consecuencias

### Positivas

- El valor se conserva por recurso al confirmar, y un intercambio a medias no deja filas.
- El libro no se reescribe ni por error del dueño; las correcciones quedan a la vista.
- Una cuenta cerrada y un cierre concurrente no pueden cruzarse.

### Negativas

- Un intercambio admite como máximo 100 asientos, en Go y en la base (`exchanges_max_entries`): la comprobación de balance corre una vez por asiento y recorre los de su intercambio, así que su coste es cuadrático y el conteo se detiene en 101 para acotarlo.
- Restaurar un respaldo exige deshabilitar los disparadores con un superusuario (B6).
- El actor lo afirma la aplicación: la base solo garantiza que existe en el espacio.

### Riesgos

- Un superusuario puede deshabilitar los disparadores con DDL deliberado (límite aceptado de ADR-0028; auditoría de DDL en B6).
- Bajo `REPEATABLE READ`, `FOR SHARE` puede dar `40001` contra un cierre; el llamador reintenta.
- El bloqueo de tabla al cambiar la escala o el tipo de un recurso no tiene prueba de carrera.

## Cumplimiento

Pruebas contra PostgreSQL real: conservación diferida y por recurso, intercambio vacío o de un asiento, montos cero, `NaN` e infinitos, escala, cuenta cerrada y su carrera con el cierre, `UPDATE`, `DELETE`, `TRUNCATE` y `TRUNCATE CASCADE` como API y administrador (también con permisos concedidos por error), asientos sobre un intercambio confirmado, compensación única, exacta y concurrente, aislamiento entre espacios, `recorded_at` y `closed_at` no falsificables, `COPY`, `ON CONFLICT` y cambio de espacio a mitad de transacción. `kernel/architecture_test.go` vigila el vocabulario del kernel.

## Referencias

- `docs/02-arquitectura/modelo-de-datos/kernel.md`, sección 2 y Reglas
- `docs/01-modelo/leyes.md`, leyes de conservación y de memoria
- ADR-0009 (libro de movimientos), ADR-0027 (roles y RLS), ADR-0028 (eventos de solo agregar)
- `docs/ingenieria/diseno/HU-kernel-exchanges-08.md`

## Enmienda 2026-11-14 · La clave del intercambio la deriva el motor

Dentro de una ejecución idempotente (ADR-0026), el motor deriva `exchanges.idempotency_key` como `<executionID>:<n>` (`executionID` es el UUID v7 de la ejecución y `n` un contador por ejecución) y `Tx.RecordExchange` rechaza con `invalid_input` la clave que traiga el manejador. La clave del cliente vive en `kernel.idempotency_records`, que vence a las 24 horas; si fuera también la del libro, una operación nueva tras el vencimiento chocaría con la unicidad eterna. La unicidad por espacio de `exchanges_idempotency_key` se conserva como defensa en profundidad: la repetición y el conflicto cortan antes del manejador, de modo que esa barrera solo atrapa a una misma ejecución que repita su clave. `Run`, sin idempotencia, conserva la clave del manejador.
