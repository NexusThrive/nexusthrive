# Kernel

## Propósito

Explicar qué hace el kernel por dentro: cómo recibe, autoriza, ejecuta, guarda y anuncia cada operación.

## Alcance

Los seis componentes del kernel y el camino de una operación. El modelo de datos está en [modelo-de-datos/kernel.md](modelo-de-datos/kernel.md).

## Conceptos principales

El kernel tiene seis componentes. Son responsabilidades, no servicios separados: corren en un solo binario de Go.

| Componente | Paquete | Responsabilidad | Nunca hace |
|---|---|---|---|
| `Model` | `model/` | Define participantes, recursos, intercambios y compromisos | Saber de rubros |
| `State` | `state/` | Guarda el presente y la memoria; aplica la conservación | Borrar historia |
| `Capability` | `capability/` | Decide quién puede hacer qué: personas, integraciones y agentes | Ejecutar acciones |
| `Coordination` | `coordination/` | Vigila el tiempo, despierta compromisos, despacha señales | Mover valor por su cuenta |
| `Execution Engine` | `execution/` | Corre cada operación autorizada en una transacción | Saltarse el permiso |
| `Extension` | `extension/` | Registra la plataforma y las distribuciones | Modificar el núcleo |

Las puertas (`gateway/`) traducen HTTP, MCP y webhooks al motor de operaciones.

## Funcionamiento

```mermaid
flowchart LR
    IN["gateway<br/>API · MCP · webhooks"] --> CAP["Capability<br/>¿puede hacerlo?"]
    CAP --> EXE["Execution Engine<br/>una transacción"]
    EXT["Extension<br/>plataforma y distribuciones"] --> EXE
    EXE --> MOD["Model"]
    EXE --> ST["State<br/>libro y eventos"]
    ST --> CO["Coordination<br/>temporizadores y outbox"]
    CO -->|"señales"| OUT["Webhooks · n8n · IA"]
```

El camino de toda operación, venga de una cara, de la consola, de una IA o de n8n:

1. Entra por una **puerta** con un token o una llave.
2. `Capability` resuelve identidad, espacio y participante, y verifica permiso y capacidad activa.
3. La **extensión** dueña de la operación la traduce a piezas del kernel.
4. `Execution Engine` abre una transacción, revisa la idempotencia y ejecuta.
5. `State` escribe intercambios, compromisos, eventos y outbox en esa misma transacción.
6. `Coordination` programa temporizadores y despacha las señales cuando la transacción confirma.

Si cualquier paso falla, no queda nada a medias.

## Permisos y verificación

`capability/` verifica dentro de la transacción que ya fijó el espacio (`state.InSpace`), con la misma RLS que la operación. No recibe un espacio aparte: el espacio es el de la transacción, y sin espacio fijado todo es `permission_denied`. El catálogo mínimo tiene 13 claves con su riesgo (`read`, `write`, `money`) y vive en `model.KernelCatalog()`; las distribuciones lo amplían con `Extend`, que nunca admite el espacio de nombres `kernel`.

`Check` evalúa en este orden y corta en el primer fallo:

| Paso | Falla con | Error |
|---|---|---|
| 1. Actor nulo, mal formado o no resoluble (también si la clave es desconocida) | Un anónimo no obtiene información | `permission_denied` |
| 2. Clave de permiso o de capacidad fuera del catálogo o mal formada | Error de código | `invalid_input` |
| 3. Sin grant vigente (vencido, revocado o participante archivado) | Mensaje único | `permission_denied` |
| 4. Espacio `suspended` o `closed` y permiso `write` o `money` | `provisioning` cuenta como `active` | `space_suspended` |
| 5. Capacidad declarada que no está `active` | | `capability_inactive` |

El actor `system` salta solo el paso 3. Para `write` y `money`, `Check` toma `FOR SHARE` sobre los grants leídos y sobre la fila del espacio: una revocación o suspensión concurrente espera a que termine la transacción. Las funciones `CheckRoleCreatable`, `CheckGrantable`, `CheckRevocable` y `CheckCapabilityEnable` aplican las reglas de escalada del ADR-0029; las operaciones las llaman en `execution-13`.

**Límite de confianza en M1:** el actor lo afirma la capa de aplicación. `Check` prueba que ese participante tiene permiso, no que quien llama sea ese participante; la puerta de B3 construye el `Actor` solo desde un token validado. Un espacio `closed` es terminal (disparador) y `kernel.capability.write` solo existe en espacios `platform` (disparador): ver [modelo de datos](modelo-de-datos/kernel.md).

## Compromisos

Un compromiso es una obligación de un deudor a un acreedor sobre un recurso, con monto y vencimiento obligatorio. El kernel, no quien llama, deriva su estado (`open`, `fulfilled`, `canceled`, `expired`), el monto cumplido y la hora de cierre; el rol de la API solo inserta compromisos abiertos. Se cumple con asientos del libro: cada cumplimiento aplica un asiento positivo de una cuenta del acreedor, una sola vez y completo, y `state.AppendFulfillment` bloquea la fila del compromiso (`FOR NO KEY UPDATE`) para que dos cumplimientos concurrentes nunca superen el monto. Cancelar exige un motivo y solo es posible mientras el compromiso está abierto. Los cumplimientos son de solo agregar y los compromisos no se borran. El vencimiento se describe en la sección siguiente. Detalle en [modelo de datos](modelo-de-datos/kernel.md) y [ADR-0033](../05-decisiones/0033-compromisos-estado-derivado-y-asiento-unico.md).

## Temporizadores y vencimiento

Cada compromiso tiene un timer de vencimiento que la base crea con el compromiso, mueve al reprogramar `due_at` (solo hacia el futuro, con `state.RescheduleCommitment`) y cancela al cumplir o cancelar. Un compromiso solo llega a `expired` cuando su timer vence según el reloj de la base: `state.FireDueTimers` (una transacción) bloquea los compromisos candidatos (`FOR NO KEY UPDATE SKIP LOCKED`, sin arrendamiento: reclamar y disparar son uno), dispara los timers vencidos, deja el compromiso `expired` y escribe el evento `kernel.commitment.expired` v1 y su fila de outbox para el suscriptor `internal`. El reloj inyectable solo puede ir por detrás del de la base. El actor es `system`; verificar `kernel.timer.process` con `Authorizer.Check` queda para execution-13. Detalle y garantías en [ADR-0034](../05-decisiones/0034-vencimiento-de-compromisos-por-timer.md).

**Estado hoy: en producción nada dispara timers.** Falta el proceso programador: un proceso periódico en `services/api` que recorra los espacios activos (el despacho entre espacios que decidirá el ADR del seguimiento 39), llame por espacio a `FireDueTimers` con `model.SystemClock` y el actor `system`, registre `scheduler_runs`, verifique `kernel.timer.process` con `Authorizer.Check` (execution-13) y no se exponga por B3. Hasta entonces un compromiso vencido sigue `open`; llega con el programador (M4 o antes). Tampoco hay recordatorios previos al vencimiento, reintentos, evento de reprogramación (seguimiento 60) ni pausa por espacio suspendido (seguimiento 62).

## Reglas e invariantes

- Las cinco [leyes](../01-modelo/leyes.md) tienen pruebas en `kernel/laws/` que nunca se desactivan.
- Una prueba de arquitectura falla si aparece vocabulario de una distribución dentro de `kernel/`.
- `kernel/` no importa `platform/` ni `distributions/`; ellas se registran en `extension/`.

## Ejemplo

El componente de ejecución procesa operaciones autorizadas. Cada operación se ejecuta dentro de una transacción para evitar que el estado quede parcialmente actualizado. Antes de ejecutarla, el kernel valida la identidad, las capacidades y las reglas correspondientes.

## Decisiones relacionadas

- [ADR-0001 · El kernel no conoce ningún mundo](../05-decisiones/0001-kernel-sin-mundo.md)
- [ADR-0009 · Libro de movimientos de doble partida](../05-decisiones/0009-libro-de-movimientos.md)
- [ADR-0032 · Catálogo de permisos y verificación](../05-decisiones/0032-catalogo-de-permisos-y-verificacion.md)
