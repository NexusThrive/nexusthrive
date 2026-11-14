# ADR-0026 · Idempotencia por registro: un registro de solo agregar por ejecución, huella canónica y bloqueo asesor

- **Estado:** Aceptada
- **Fecha:** 2026-11-14
- **Responsables:** santy401

## Contexto

HU-kernel-idempotency-14 cierra la regla 6 de `AGENTS.md`: las operaciones de dinero son siempre idempotentes. Repetir una solicitud con la misma clave y el mismo cuerpo devuelve la respuesta original sin volver a escribir; la misma clave con otro cuerpo es un error; dos solicitudes simultáneas con la misma clave escriben una sola vez; y el registro vence a las 24 horas. Hasta ahora la única barrera era `exchanges.idempotency_key`, única para siempre por espacio (ADR-0030), y el ADR-0035 solo declaraba y validaba la idempotencia. El número 0026 estaba reservado desde el inicio para esta decisión. Siguen vigentes los ADR-0027 (roles y RLS), ADR-0028 (inmutabilidad en dos capas), ADR-0030 (libro), ADR-0034 (reloj inyectable por detrás del de la base) y ADR-0035 (motor).

## Decisión

**1. Un registro por (espacio, clave), no solo la clave del libro.** `kernel.idempotency_records` guarda, por cada ejecución idempotente, el espacio, la clave, la operación, el actor, la huella de la solicitud, la respuesta y las horas de creación y vencimiento. La clave del libro sirve para una sola cosa (un intercambio) y es única para siempre; la idempotencia de una operación necesita respuesta, huella y vencimiento, y no todas las operaciones escriben un intercambio.

**2. Huella SHA-256 sobre una serialización canónica, sin guardar el cuerpo.** `model.RequestHash` calcula SHA-256 de campos con prefijo de largo de 8 bytes: la etiqueta `nexusthrive.idempotency.v1`, la operación, el tipo y el participante del actor y el cuerpo canónico. `model.CanonicalJSON` ordena las claves de los objetos byte a byte, quita espacios, conserva el texto literal de los números (`1` y `1.0` son cuerpos distintos: la salida es conservadora) y rechaza con `invalid_input` el UTF-8 inválido, las claves duplicadas (el decodificador estándar se queda con la última y haría ambiguo el cuerpo), la profundidad mayor a 64, más de 1 MiB, bytes sobrantes y el cuerpo vacío. La operación y el actor van en la huella para que otra operación u otro actor con la misma clave reciba `idempotency_conflict` y no la respuesta ajena. El cuerpo no se guarda: puede tener datos sensibles, ocupa espacio y no hace falta para comparar.

**3. Bloqueo asesor por (espacio, clave), no `ON CONFLICT` ni `FOR UPDATE`.** El registro no existe antes de que corra el manejador y no puede actualizarse después (es de solo agregar). `ON CONFLICT DO NOTHING` exigiría insertar antes del manejador, con la respuesta aún desconocida; `FOR UPDATE` no bloquea una fila que no existe, y dos solicitudes simultáneas verían "no hay registro" y ejecutarían dos veces. `kernel.idempotency_lock(key)` toma `pg_advisory_xact_lock` sobre el hash del espacio actual y la clave; lo usan Go y el disparador de inserción, con una sola definición. El bloqueo dura hasta la confirmación: la segunda solicitud espera al ganador, relee y devuelve la misma respuesta, o ejecuta si el ganador falló. Un choque de hash solo hace esperar de más, nunca decide.

**4. Tabla de solo agregar con generaciones.** Reemplazar un registro vencido sería un `UPDATE` o un `DELETE`, y la inmutabilidad en dos capas (ADR-0028) no admite ninguno. La llave primaria es (`space_id`, `key`, `generation`): la ejecución nueva tras el vencimiento inserta la generación siguiente y la vigente es la mayor. `generation` y `expires_at` los estampa un disparador `BEFORE INSERT`; ningún llamador los escribe (el rol de la API no tiene el privilegio de columna). No hay `UPDATE`, `DELETE` ni `TRUNCATE` (disparadores de fila y de sentencia, también en cascada). La purga de registros vencidos queda diferida: una migración futura de retención, con su ADR, añadirá un borrado acotado a vencidos.

**5. Código tipado `idempotency_conflict`.** La misma clave con otra huella devuelve `idempotency_conflict` con un mensaje fijo que no revela nada del registro. No es `already_done` ("ya se hizo": aquí la solicitud es distinta, no repetida) ni `invalid_input` (el formato es correcto). Enmienda el ADR-0004 y `docs/02-arquitectura/contrato.md`.

**6. La clave del intercambio la deriva el motor.** Con la clave del cliente como `exchanges.idempotency_key`, la ejecución nueva tras 24 horas fallaría contra la unicidad eterna del libro. El motor estampa `<executionID>:<n>` (`executionID` es un UUID v7 por ejecución y `n` un contador por ejecución) y `Tx.RecordExchange` rechaza con `invalid_input` la clave que traiga el manejador. La repetición y el conflicto cortan antes del manejador; la unicidad del libro queda como defensa en profundidad. Enmienda el ADR-0030.

**7. El reloj inyectable tiene una ventana de 5 minutos.** `created_at` lo aporta Go con el reloj inyectable, porque la base no lo ve y los criterios de vencimiento exigen probarlo sin dormir 24 horas. El disparador de inserción lo acota por los dos lados, también para el administrador: `now() - 5 minutos <= created_at <= now()` (`restrict_violation` fuera de la ventana; la constante `model.IdempotencyClockTolerance` es la misma de la base y una prueba las compara). Un `created_at` más antiguo nacería con menos de 24 horas de vida o ya vencido, y una repetición ejecutaría el manejador otra vez (doble cobro): por eso no hay holgura hacia atrás. Go escribe `LEAST(reloj, now())`, así que un reloj adelantado, aun por milisegundos entre dos hosts, no hace fallar la escritura ni alarga el registro. Antes de ejecutar el manejador el motor comprueba el reloj (`state.CheckIdempotencyClock`): el reloj en cero, o atrasado respecto de `now()` de la base por más de la tolerancia, falla con `invalid_input` sin ejecutar el manejador y sin escribir, de modo que el desfase se ve y no debilita la idempotencia en silencio; una repetición que encuentra su registro vigente no necesita el reloj. En lectura, un registro vence cuando `expires_at <= LEAST(reloj, now())`: con el reloj atrasado dentro de la tolerancia el registro se considera vigente hasta 5 minutos más. **Límite residual:** un nodo cuyo reloj esté atrasado dentro de la tolerancia escribe registros que viven hasta 5 minutos menos de lo previsto frente a la base (vencen a las 24 horas menos el desfase, medido con `now()`), y otro nodo puede ver el vencimiento esa fracción antes; por eso la ventana es pequeña. Quien tenga el rol de la API puede, como mucho, fechar un registro propio dentro de la ventana. Es una desviación consciente de la regla "la API no fija columnas del kernel", con el precedente de `next_attempt_at` del outbox y del techo `LEAST` de los timers.

**Aislamiento.** El bloqueo de la clave solo sirve si la lectura posterior ve lo que confirmó el ganador: el motor exige el nivel `READ COMMITTED` (lee `transaction_isolation`; no lo cambia, porque ya no se puede tras la primera consulta) y falla con `rule_violation` antes de ejecutar nada si la base entrega otro.

**8. Respuesta y solicitud acotadas.** La respuesta es un objeto JSON y la base mide su límite de 64 KiB sobre el texto de `jsonb`, que añade un espacio tras cada dos puntos y cada coma: una respuesta cuyo texto crudo cabe puede no caber guardada, y entonces es `invalid_input` sin escribir nada. El cuerpo de la solicitud admite hasta 1 MiB y se rechazan los escapes de sustitutos UTF-16 sueltos, que el decodificador convertiría en U+FFFD y harían colisionar cuerpos distintos. La clave cumple `^[A-Za-z0-9._:-]{1,128}$`. `Call.Request` es obligatorio para operaciones idempotentes y lo llenará la fase B3 con el cuerpo recibido; el motor calcula la huella.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Solo `exchanges.idempotency_key` | No guarda respuesta, huella ni vencimiento, y no sirve a una operación que no escribe intercambio. |
| `ON CONFLICT DO NOTHING` sobre el registro | Obliga a insertar antes del manejador, con la respuesta desconocida y sin poder actualizarla después. |
| `SELECT ... FOR UPDATE` | No bloquea una fila que no existe: dos solicitudes simultáneas ejecutarían dos veces. |
| Reemplazar el registro vencido con `UPDATE` o `DELETE` | Rompe la inmutabilidad en dos capas y abre una ruta de borrado. |
| Relajar la unicidad de `exchanges.idempotency_key` | Quita la segunda barrera y debilita la ley de memoria. |
| `created_at` con `now()` de la base | Impide probar el vencimiento sin dormir 24 horas. |
| Guardar el cuerpo de la solicitud | Puede tener datos sensibles y ocupa espacio; la huella basta. |
| `already_done` o `invalid_input` para el conflicto | Significan otra cosa; el contrato pide un código propio. |

## Consecuencias

### Positivas

- Una solicitud repetida devuelve, byte a byte, la respuesta guardada, y dos simultáneas escriben una sola vez.
- El vencimiento se prueba sin esperar, y una clave vieja no bloquea una operación nueva.
- Ningún camino de borrado ni de actualización: el registro es memoria.

### Negativas

- La tabla crece una fila por ejecución, y por clave tras cada vencimiento, hasta que exista la retención (seguimiento nuevo, B6).
- El `created_at` aportado por Go es una desviación acotada, por los dos lados, de la regla de columnas del kernel; un reloj atrasado más de 5 minutos hace fallar las operaciones idempotentes con `invalid_input` hasta que se corrija.
- La segunda solicitud espera el manejador completo del ganador; el `ctx` de B3 acota la espera.
- `Call.Request` depende de B3 (seguimiento nuevo).

### Riesgos

- El rol de la API es el proceso del kernel (seguimiento 42): quien lo controle puede insertar registros falsos en su propio espacio (provoca conflicto o repite una respuesta inventada) y no cruza espacios; quedan `CHECK`, llave primaria, clave foránea compuesta, RLS y grants.
- Un administrador puede deshabilitar disparadores (límite del ADR-0028); quedan `CHECK`, llave primaria y clave foránea.
- Un administrador con su propio `pg_advisory_lock` puede detener una clave: es negación de servicio del operador, no del cliente.
- Cambiar el formato de la huella exige versionar la etiqueta (seguimiento nuevo).

## Cumplimiento

Pruebas de `kernel/model` (huella, serialización canónica, vector fijo), `kernel/db` (esquema, grants, disparadores y ataques con el rol de la API y el administrador), `kernel/state` y `kernel/execution` (repetición, conflicto, concurrencia real, vencimiento, fallos), sin `t.Skip`. La prueba de arquitectura vigila el vocabulario del kernel y `adr_test.go` valida el formato.

## Referencias

- `docs/ingenieria/diseno/HU-kernel-idempotency-14.md`
- ADR-0004, ADR-0027, ADR-0028, ADR-0030, ADR-0034, ADR-0035
- `docs/02-arquitectura/contrato.md`, `docs/02-arquitectura/modelo-de-datos/kernel.md`
