# Seguimientos del sprint S01

Observaciones no bloqueantes de las revisiones que no entran en la historia donde se hallaron. Cada una indica dónde se resuelve.

| # | Origen | Seguimiento | Se resuelve en |
| --- | --- | --- | --- |
| 1 | spaces-03 | La contraseña del rol de la API (`API_DB_PASSWORD`) puede quedar en los logs del servidor al aplicarse con `ALTER ROLE`. El ADR-0027 propone calcular el verificador SCRAM en Go. Una variable vacía no revoca un LOGIN ya concedido. | Antes del despliegue de staging (M2) |
| 2 | spaces-03 | `SET ROLE` en las pruebas no ejercita un inicio de sesión real con el rol limitado. | Prueba en staging (M2) y B6 |
| 3 | participants-04 | El argumento del ADR-0027 sobre UUID v7 como barrera ("no se adivina") es débil: los UUID no son secretos. Debe sostenerse en que solo el actor `system` crea participantes. | Enmienda del ADR-0027 en la historia `laws-15` |
| 4 | participants-04 | Precondición de B3: `IdentityID` nunca debe venir de la petición de un cliente; debe resolverse en el servidor (invitación verificada o sesión). Una prueba debe fijar el comportamiento aceptado en M1 (dos espacios con participante sobre la misma identidad leen el mismo correo; vincular un UUID ajeno tiene éxito). | Prueba en `laws-15`; precondición en el backlog de B3 |
| 5 | participants-04 | `GRANT UPDATE (status, archived_at)` permite reactivar un participante archivado. Reactivar debe ser una operación explícita cuando llegue. | Backlog posterior a M1 |
| 6 | participants-04 | El `INSERT` libre en `identities` permite reservar pares `(provider, subject)` y los errores `23505` y `23503` revelan existencia. Hay que decidir la vía de buscar o crear sin enumeración, con límite de tasa. | B3 (ADR propio) |
| 7 | participants-04 | Faltan pruebas directas por SQL de los `CHECK` de `participants` (archivado coherente, pareja de documento, país). | `laws-15` |
| 8 | testinfra-02 | Faltan doc comments en helpers de `dbtest`; con muchos paquetes en paralelo PostgreSQL podría agotar conexiones. | Pulido al cierre de M1 |
| 9 | spaces-03 | La prueba `TestAPIRoleIsLimited` asume que el rol sigue sin inicio de sesión; fallaría en un clúster de desarrollo donde se aplicó la contraseña. | Pulido al cierre de M1 |
| 10 | migrations-01 | `CREATE SCHEMA IF NOT EXISTS` se ejecuta antes del bloqueo consultivo; dos despliegues simultáneos podrían chocar. Verificar que `Up` funciona sobre un esquema creado por la rama sin archivos. | Antes de varias instancias de la API (B6) |
| 11 | todas | El backlog sigue marcando `migrations-01` como "Lista" y la tabla de definición de listo está sin actualizar. | Cierre del sprint |
| 12 | events-09 | `payload` no tiene tamaño máximo; el límite pertenece al contrato de B3. | B3 (contratos de eventos) |
| 13 | events-09 | Los consumidores por cursor de `seq` deben reanudar con traslape y deduplicar por `id`, o usar un horizonte de transacción (ADR-0028). | B3 (ADR propio) |
| 14 | events-09 | `AppendEvent` hace una lectura previa de `current_space_id()` por evento (un viaje de ida y vuelta más); si se vuelve un coste, fusionarla con el `INSERT`. | Optimización posterior |
| 15 | events-09 | El `GRANT INSERT` por columnas de `kernel.events` debe actualizarse a mano al añadir columnas; la prueba con `column_privileges` lo detecta. Mencionarlo en el ADR-0028. | Enmienda del ADR-0028 en `laws-15` |
| 16 | events-09 | `TestEventsAPIRoleCannotSetSeq` itera sobre un `map` (orden no determinista, sin efecto en el resultado). | Pulido al cierre de M1 |
| 17 | resources-07 | Un disparador en exchanges-08 debe impedir cambiar `scale` o `kind` de un recurso cuando existan asientos (la API ya no puede, pero el dueño sí). | Resuelto en exchanges-08 |
| 18 | resources-07 | Abrir una cuenta para un titular archivado y cerrar una cuenta con hijas abiertas no está prohibido; decidir si se prohíbe. | exchanges-08 o posterior |
| 19 | resources-07 | `closed_at` es falsificable al cerrar una cuenta (el rol de la API puede escribir cualquier fecha). El kernel debe fijar `NEW.closed_at := now()` en el paso de `open` a `closed`, con una prueba con el rol de la API. | Resuelto en exchanges-08 |
| 20 | grants-05 | La prueba de escalada real (otorgante sin `kernel.grant.write` o con menos permisos) y el uso de `access_audit` llegan con permissions-06 y B4. | `permissions-06` y B4 |
| 21 | grants-05 | El tope de duración de un acceso de soporte no está definido (solo se exige `expires_at`); proponer un máximo. | B4 |
| 22 | grants-05 | `space_capabilities` no tiene `UPDATE`: suspender o reactivar una capacidad es otra historia. | Historia de capacidades |
| 23 | grants-05 | Un grant vencido pero no revocado bloquea otorgar de nuevo el mismo rol (índice parcial); hay que revocar primero. Decidir si la ejecución revoca al vencer. | `execution-13` |
| 24 | grants-05 | Resuelto en la misma historia: `kind` de `spaces` es inmutable (privilegio `UPDATE` por columnas y disparador, ADR-0029). | Resuelto |
| 25 | grants-05 | `nexusthrive_api` es un rol global del clúster: si `kernel/state` y `kernel/db` corren en paralelo, una prueba de contraseña puede dejarle LOGIN y hacer fallar `TestAPIRoleIsLimited`. En CI no ocurre (base nueva). | Pulido al cierre de M1 |
| 26 | grants-05 | Nada impide reactivar un espacio cerrado (`closed` a `active`) ni limpiar `closed_at` con UPDATE de columnas del rol de la API. Falta una regla de transición de estado. | Ciclo de vida de espacios (B4) o `permissions-06` |
| 27 | grants-05 | Resuelto en la misma historia: el `INSERT` del rol de la API es por columnas y un disparador impide crear espacios `platform` con ese rol (ADR-0029). | Resuelto |
| 28 | grants-05 | La API puede escribir `access_audit` y `grants` atribuyendo la acción a un participante de un espacio `platform` si conoce su UUID: las FK no aplican RLS y los CHECK solo validan el espacio, no la identidad del actor. Hay que decidir en un ADR quién certifica al actor de plataforma (autenticación de actores). Atenuantes: el UUID del staff no es legible con la RLS y en M1 solo escribe `system`. | `permissions-06`, `execution-13` y B3 |
| 29 | exchanges-08 | El actor de un intercambio lo afirma la aplicación: la base solo garantiza que el participante existe en el espacio, y la API podría escribir cualquiera de ellos. El vínculo con la identidad llega con los permisos. | `execution-13` y `permissions-06` |
| 30 | exchanges-08 | Una cuenta cerrada después del intercambio original impide compensarlo (el criterio 5 no admite excepción y las cuentas no se reabren). Decidir en un ADR si la compensación puede usar cuentas cerradas. | ADR posterior, si aparece el caso |
| 31 | exchanges-08 | Restaurar un respaldo necesita deshabilitar los disparadores de `entries` (`--disable-triggers` con superusuario), porque rechazan recargar asientos antiguos. Documentar el procedimiento. | B6 (respaldo y restauración) |
| 32 | exchanges-08 | El disparador de `entries` corre por fila y recorre los asientos de su intercambio: coste O(N cuadrado), acotado a 100 asientos por intercambio. Medir antes de subir el límite. | Optimización posterior |
| 33 | exchanges-08 | `FOR SHARE` sobre la cuenta puede dar `40001` contra un cierre concurrente bajo `REPEATABLE READ`; el error no se traduce y el llamador reintenta. Hoy todo corre en `READ COMMITTED`. | `execution-13` |
| 34 | exchanges-08 | El `LOCK TABLE kernel.entries IN SHARE MODE` del cambio de `scale` o `kind` de un recurso (operación excepcional del dueño) no tiene prueba de carrera. | Pulido al cierre de M1 |
| 35 | exchanges-08 | Nada impide compensar una compensación (R3 compensa a R2 que compensa a R1): no rompe la conservación pero recrea el efecto del original. Decidir en un ADR si se prohíbe (inclinación: prohibirla). Tampoco la base exige `kind = kernel.exchange.reversal` cuando `reverses_exchange_id` no es nulo; solo Go lo garantiza. | `laws-15` o ADR propio |
| 36 | exchanges-08 | `AppendExchange` devuelve las restricciones a diferido al terminar (`SET CONSTRAINTS DEFERRED`) aunque el llamador las hubiera puesto en inmediato; documentarlo con un comentario. Falta una prueba Up-Down-Up de la migración 00009 cuando exista ejecutor de Down. | Pulido al cierre de M1 |
| 37 | exchanges-08 | `translateExchangeError` mapea todo `23502` a `rule_violation` con el mensaje "no current space"; para un actor nulo el mensaje es incorrecto (el rechazo sí es correcto). Mapear por nombre de columna. | Pulido al cierre de M1 |
