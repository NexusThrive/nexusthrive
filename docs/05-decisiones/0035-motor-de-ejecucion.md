# ADR-0035 · Motor de ejecución: una operación, una transacción, verificación previa y `Tx` mínima sin SQL crudo

- **Estado:** Aceptada
- **Fecha:** 2026-11-14
- **Responsables:** santy401

## Contexto

HU-kernel-execution-13 cierra la parte de la ley de permiso y de la ley de memoria que aún dependía de la disciplina de quien llamaba: hasta ahora `Authorizer.Check` (ADR-0032) existe, pero nada obliga a invocarlo, y las funciones de `kernel/state` escriben sin verificar permiso. Antes de escribir código (regla 12 de `AGENTS.md`) hay que decidir cómo se declara una operación, dónde corre la verificación, qué puede tocar un manejador y qué queda como precondición de B3. Siguen vigentes el ADR-0027 (roles y RLS), el ADR-0028 (eventos), el ADR-0030 (libro), el ADR-0031 (outbox), el ADR-0032 (permisos) y el ADR-0034 (timers).

## Decisión

**Una operación se declara en Go y se registra al arrancar.** `execution.Operation` lleva nombre (minúsculas con puntos; el primer segmento es su espacio de nombres; `kernel` está reservado a las operaciones del propio kernel), una lista de claves de permiso (`Permissions`, todas del catálogo, al menos una), una capacidad opcional, el riesgo y la declaración de idempotencia. `NewEngine` valida todas y falla con `invalid_input` si hay un nombre inválido, duplicado o con prefijo `kernel.`, permisos vacíos, repetidos o fuera del catálogo, una capacidad mal formada, un riesgo distinto del mayor riesgo de sus permisos (una operación de dinero no se declara `write` para esquivar la regla) o una operación `money` sin `Idempotent`. La idempotencia se declara aquí y la implementa `Engine.RunIdempotent` (HU-kernel-idempotency-14, [ADR-0026](0026-idempotencia-por-registro.md)); `Run` rechaza con `invalid_input` una operación declarada `Idempotent`.

**Una operación, una transacción, con la verificación dentro.** `Engine.Run` valida la llamada antes de abrir nada (identificador de solicitud con patrón acotado, manejador no nulo, operación registrada), abre `state.InSpace` y, dentro de esa misma transacción y antes de cualquier escritura, ejecuta `Authorizer.Check` por cada clave de `Permissions` (la capacidad se verifica en la primera). Que `Check` comparta la transacción es lo que hace que sus bloqueos `FOR SHARE` sobre grants y espacio duren hasta el `COMMIT`: una revocación o una suspensión concurrente espera a la operación. Las operaciones `read` además fijan `SET TRANSACTION READ ONLY` como defensa en profundidad, porque el rol de la API puede escribir y no distingue una lectura. El manejador corre con una `Tx`; al terminar, con éxito o error, la `Tx` se cierra y después se confirma. Un error de base que el manejador trague no confirma: el servidor deja la transacción abortada y el `COMMIT` devuelve el error de reversión.

**La `Tx` es mínima y no expone SQL.** Ofrece solo primitivas de estado (`RecordExchange`, `CreateCommitment`, `AppendEvent` con outbox, `ReadEvents`); ninguna devuelve ni acepta un `*sql.Tx`. Cada método exige que su permiso esté entre los declarados por la operación. El actor y el `request_id` se estampan desde la llamada: la entrada de evento no los tiene, así que un manejador no atribuye un evento a otro. El tipo de un evento y el `Kind` de un intercambio o compromiso deben empezar con el espacio de nombres de la operación, de modo que una extensión no puede falsificar `kernel.commitment.expired`. Una `Tx` usada fuera de su manejador devuelve `rule_violation` y no escribe. Decisión consciente: `AppendEvent` no exige un permiso declarado propio, porque el catálogo no tiene una clave `event.write`; basta una operación `write` o `money` y que el tipo esté en su espacio de nombres. Una `Tx` copiada por valor comparte el cierre con la original.

**El `request_id` vive en el evento.** Intercambios y compromisos no tienen la columna; el outbox cuelga del evento por `event_seq` y el `payload` nombra los identificadores del intercambio y del compromiso. No hay migración. Es una lectura del criterio 1 aceptada por el responsable.

**Un pánico es un error sin tipo.** `Run` lo recupera, la reversión ya ocurrió porque la transacción se deshace mientras el pánico desenrolla, y devuelve `*PanicError` con la operación, el valor y la pila. Su `Error()` solo nombra la operación (ni el valor ni la pila, que quedan en sus campos para los registros) ni hereda el código tipado de un valor que sea un error del contrato. No hay código "interno" en el contrato: B3 debe traducirlo a una respuesta genérica sin mensaje.

**Clasificación de errores.** En este orden: un error tipado pasa sin envolver; `*PanicError` pasa; los errores de contexto se envuelven sin tipar; `23001` (`restrict_violation`) es `permission_denied`; los fallos de `COMMIT` pasan por `state.TranslateCommitError` (restricciones diferidas conocidas a `rule_violation`); `42501` (privilegio faltante), `40001` y `40P01` se envuelven sin tipar: un privilegio faltante nunca se disfraza de `rule_violation` o `not_found`, y no hay reintento automático porque el contrato no tiene código reintentable y reejecutar el manejador repetiría efectos.

**Operaciones propias del kernel.** `kernel.timer.fire` (permiso `kernel.timer.process`) y `kernel.outbox.claim` (permiso `kernel.outbox.claim`), ambas `write`, son los únicos caminos de producción para disparar timers y reclamar el outbox, con el reloj del `Authorizer`. Por ser `write`, un espacio suspendido las pausa. Las variantes `Append*` de `kernel/state` dejan de exigir el actor `system` (el permiso lo decide `Check`); los envoltorios con pool lo siguen exigiendo y quedan para pruebas y arranque. `Engine.Run`, la ruta exportada, rechaza con `invalid_input` (la misma respuesta de una operación no registrada, antes de abrir nada) todo nombre que, ignorando mayúsculas y espacios, empiece por `kernel`: un actor con `kernel.timer.process` no puede llamar a `kernel.timer.fire` con un manejador propio para escribir eventos del espacio de nombres `kernel`. `FireDueTimers` y `ClaimOutbox` usan una ruta interna no exportada con un manejador fijado por el motor.

**La ruta alternativa existe y se acota.** El motor es código: quien tenga el pool con el rol de la API puede llamar a `state.*` o ejecutar SQL y saltarse `Check`; la base solo impide lo que sus disparadores y RLS impiden. En M1 es aceptable porque el único proceso con el pool es el kernel. Mitigaciones: la `Tx` no expone SQL, el pool del motor no se exporta y una prueba de arquitectura prohíbe que cualquier paquete fuera de `kernel/state` y `kernel/execution` importe `kernel/state`.

**Precondiciones de B3.** Las puertas y las extensiones reciben solo el `Engine`, nunca el pool ni `kernel/state`. El `Actor` se construye únicamente desde un token validado y `ActorSystem` nunca desde una petición (seguimientos 28, 29 y 46). `ClaimOutbox`, `MarkOutboxSent` y `MarkOutboxFailed` no se exponen a clientes (seguimiento 42). `*PanicError` se traduce a un error genérico sin mensaje ni pila. Un `Engine` por proceso del kernel.

**No se crea `kernel/extension`.** Nadie lo registra en M1: la plataforma llega en B4 y la primera distribución en la etapa 2. Cuando exista devolverá `[]Operation` y un catálogo ampliado con `Extend`, y los entregará a `NewEngine`; el motor no cambia.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Entregar al manejador el `*sql.Tx` | Permitiría SQL crudo que se salte los métodos con permiso declarado y el estampado del actor. |
| Verificar el permiso en una transacción previa | Los bloqueos de `Check` terminarían antes de la operación: una revocación concurrente no esperaría. |
| Una sola clave de permiso por operación | El criterio 1 pide dos permisos a la vez; una lista se verifica clave por clave. |
| Columna `request_id` en intercambios y compromisos | Exige disparadores de inmutabilidad y privilegios por columna que rehacen pruebas de las migraciones 00009 y 00012 por una trazabilidad que el evento ya da. |
| Reintentar ante `40001` y `40P01` | Reejecutar el manejador repite efectos y el contrato no tiene un código reintentable. |
| Código tipado para el pánico | El contrato no tiene código interno; un código existente mentiría sobre la causa. |
| Crear `kernel/extension` ahora | Regla 11: nadie lo usa en M1. |

## Consecuencias

### Positivas

- La ley de permiso queda aplicada de extremo a extremo para toda operación que pase por el motor, con la verificación y la escritura atómicas.
- Los cuatro efectos de una operación (intercambio, compromiso, evento y outbox) nacen juntos o ninguno, con evento atribuido al actor y a la solicitud.
- Cierra los seguimientos 20, 42, 44, 45 (el permiso), 49, 50 y 62.

### Negativas

- La ruta alternativa por el pool sigue existiendo y depende de la disciplina de B3 y de la prueba de arquitectura.
- Un actor afirmado por la aplicación sigue sin ser verificado por la base (seguimientos 28 y 29).
- No hay reintento ante conflictos de serialización.

### Riesgos

- Un pánico llega a B3 como error sin tipo: si una puerta lo trata como un error comprensible puede filtrar el valor.
- Una operación futura que declare un riesgo correcto pero use primitivas de otra operación queda acotada solo por los permisos declarados.

## Cumplimiento

Pruebas de `kernel/capability`, `kernel/execution`, `kernel/state` y `kernel/laws` contra PostgreSQL real, sin `t.Skip`: operación sin permiso, con actor nulo, de otro espacio, con permiso vencido o revocado; verificación en la misma transacción y antes de escribir; `Tx` usada tras terminar; pánico sin transacción abierta; eventos falsificados; registro de operaciones; lecturas que intentan escribir; y la prueba de arquitectura de importadores de `kernel/state`. `adr_test.go` valida el formato.

## Referencias

- `docs/ingenieria/diseno/HU-kernel-execution-13.md`
- ADR-0027, ADR-0028, ADR-0030, ADR-0031, ADR-0032, ADR-0034
- `docs/02-arquitectura/kernel.md`, `docs/02-arquitectura/contrato.md`
