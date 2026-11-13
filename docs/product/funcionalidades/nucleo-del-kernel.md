# Funcionalidad: Núcleo del kernel (M1 = B2)

Fuente: petición de la fase M1 (B2 de `docs/02-arquitectura/hoja-de-ruta.md`), alcance MVP de `docs/05-decisiones/0018-recorte-de-la-etapa-1.md` y `docs/product/contexto-dominio.md`.

## Problema a resolver

NexusThrive promete un kernel que estructura ecosistemas con siete primitivas y cinco leyes, pero hoy el kernel no ejecuta nada. Solo existen `kernel/go.mod`, `kernel/doc.go` y una prueba de arquitectura. No hay migraciones, ni libro, ni compromisos, ni eventos, ni herramienta de migraciones elegida. Mientras eso sea así:

- Las leyes (conservación, memoria, compromiso, permiso, movimiento) son texto en `docs/01-modelo/leyes.md`, no garantías. Nadie puede construir encima sin riesgo de descubrir tarde que el dinero no cuadra o que un espacio ve datos de otro.
- Las fases B3 (puertas), B4 (plataforma) y B5 (consola) no tienen nada sobre qué apoyarse. Todo el resto de la etapa 1 está bloqueado por esta fase.

## Usuario objetivo

M1 no tiene usuario final. Sus usuarios directos son:

- **Equipo de ingeniería de NexusThrive** (quien construye B3, B4, B5 y después Thrive Business). Necesita una base confiable y probada.
- **Futuro equipo NexusThrive (staff)**, a quien B4 y B5 darán herramientas. Se beneficia de forma indirecta: su acceso privilegiado y auditado depende de los grants y del aislamiento que M1 deja listos.
- **Dueños de espacios** (roles `owner`, `admin` y similares del modelo de datos), solo de forma indirecta. Lo que M1 les garantiza es que nadie ajeno ve ni altera sus datos y que su historial no se reescribe.

## Resultado esperado (outcome)

Cuando M1 termine, se puede afirmar y demostrar con pruebas automáticas:

1. Las cinco leyes se cumplen y lo verifican pruebas en `kernel/laws` que nunca se omiten.
2. Un intercambio y un compromiso de prueba se escriben en una sola transacción junto con su evento y su fila de outbox. Si algo falla, no queda nada escrito.
3. Ningún dato cruza entre espacios. Está probado con dos espacios, con la RLS activa y con el rol de la API sin forma de saltársela.
4. El esquema `kernel` se crea desde cero, con una sola orden, a partir de migraciones en SQL puro.
5. B3 puede empezar sin rehacer nada del kernel.

## Alcance

### Incluido

- **Las siete primitivas** como tipos y tablas del esquema `kernel`: Participante, Recurso, Intercambio, Compromiso, Evento, Tiempo y Regla (Grant/Capability).
- **Espacios, identidad y permisos:** espacios, identidades, participantes, roles, grants y capacidades activas por espacio, con verificación de permiso antes de toda operación. Incluye el catálogo mínimo de permisos que el kernel necesita para sus propias operaciones.
- **Libro de doble partida:** recursos, cuentas, intercambios y asientos. Los asientos de un intercambio suman cero por recurso. Las correcciones son intercambios compensatorios (`reverses_exchange_id`). Dinero con decimales exactos.
- **Compromisos:** creación, cumplimiento (con `fulfillments`), cancelación y vencimiento. Estados `open`, `fulfilled`, `canceled` y `expired`, como en el modelo de datos. `due_at` obligatorio y `fulfilled_amount <= amount`.
- **Tiempo (ley de movimiento):** al crear o modificar un compromiso con vencimiento se crea o reprograma su `timer` en la misma transacción. Un reloj inyectable permite probar que un timer vencido se dispara y que el compromiso pasa a `expired` sin esperar tiempo real.
- **Eventos con outbox:** eventos de solo agregar con `seq`, y filas de outbox escritas en la misma transacción. En M1 el outbox solo se escribe y se reclama (un reclamo concurrente no entrega dos veces la misma fila). No hay consumidor.
- **Idempotencia:** `idempotency_records` por espacio y clave. Repetir la clave con el mismo cuerpo devuelve la respuesta guardada. Repetirla con otro cuerpo devuelve un error tipado.
- **Aislamiento entre espacios:** `space_id` en toda tabla de datos de espacio y RLS. El `space_id` se fija con `SET LOCAL` en cada transacción. El rol de base de datos de la API no puede saltarse la RLS.
- **Motor de ejecución:** una operación, una transacción (libro, compromisos, eventos y outbox juntos o ninguno).
- **Migraciones del esquema `kernel`** en SQL puro. La herramienta se decide en un ADR dentro de la primera historia, antes del código (regla 12).
- **Infraestructura de pruebas:** pruebas de integración contra el PostgreSQL de `task infra:up` y, en CI, contra PostgreSQL como servicio. Datos creados por la propia prueba y tipos neutros, sin vocabulario de negocio.
- **Documentación actualizada** en el mismo cambio (regla de un PR por historia), incluidas las referencias a Testcontainers de `docs/07-ingenieria/testing.md` y a B6 en la hoja de ruta, que dejan de coincidir con estas decisiones.

### Excluido explícitamente

- Operaciones y eventos en `contracts/` (proto y JSON Schema). Es B3. Los eventos de M1 se validan a nivel de paquete Go, sin contrato publicado.
- Tablas de webhooks (`webhook_endpoints`, `webhook_deliveries`), avisos (`notices`), llaves (`api_keys`) y archivos (`files`). Pertenecen a B3.
- Consumidor del outbox, entregas firmadas, n8n y MCP. Es B3.
- Puertas (Connect API), validación de tokens de Clerk. Es B3.
- Capa de plataforma: planes, suscripciones, cobros, códigos de beneficio, acceso de soporte de NexusThrive. Es B4. M1 no implementa el bypass de RLS del staff.
- Consola y cualquier app. Es B5.
- Proceso programador en el binario `services/api` y `scheduler_runs` en producción: M1 prueba el disparo de timers con reloj inyectable a nivel de paquete. El proceso real es trabajo posterior (decisión abierta, ver abajo).
- Redis. No se usa en M1.
- Particionado del libro y reportes de ledger a gran escala (pospuesto por ADR-0018).
- Cualquier cosa de Thrive Business (etapa 2).
- Batería completa de aislamiento, MFA de staff, observabilidad, respaldo y restauración. Siguen en B6; M1 solo adelanta la prueba de aislamiento sobre sus propias tablas.

## Valor y prioridad

**Valor para el usuario (ingeniería).** Elimina el bloqueo de toda la etapa 1: sin libro, compromisos, eventos y permisos, B3, B4 y B5 no pueden empezar. Con las leyes ya verificadas por pruebas, los errores de integridad se detectan en el momento en que se introducen y no cuando hay datos de clientes.

**Valor para el negocio.** El producto vende confianza: que el valor se conserva, que la historia no se reescribe y que cada espacio es privado. Probarlo desde la primera fase es más barato que arreglarlo cuando haya espacios reales. Adelantar el aislamiento a M1 evita construir B3 y B4 sobre un modelo de datos que luego haya que reescribir para agregar `space_id` y RLS.

**Esfuerzo relativo:** XL. Es la fase más grande de la base: esquema completo, leyes, motor de ejecución, RLS y herramientas de prueba. Debe dividirse en historias finas, cada una con su rama `feature/HU-kernel-...-NN`, y la primera lleva el ADR de migraciones y la infraestructura de pruebas.

**Prioridad: Must.** Es la ruta crítica de la etapa 1 y de toda la hoja de ruta. Nada de lo demás tiene valor sin ella.

Orden de entrega sugerido para que cada historia sea un corte vertical demostrable (el detalle y las fechas son del Analista de Requisitos y del Project Manager):

1. ADR de migraciones, esquema base, infraestructura de pruebas contra PostgreSQL y CI con servicio.
2. Espacios, identidades, participantes, grants y RLS con `SET LOCAL`, más prueba de aislamiento.
3. Libro de doble partida con conservación y memoria.
4. Compromisos y timers, con reloj inyectable.
5. Eventos y outbox, motor de ejecución con transacción única e idempotencia.
6. Cierre: pruebas de las cinco leyes en `kernel/laws` y la prueba de extremo a extremo (intercambio, compromiso, evento y outbox en una transacción).

## Criterios de éxito

M1 está terminada cuando se cumplen todos, verificados por pruebas automáticas en CI salvo indicación:

1. **Leyes en verde.** Existe al menos una prueba por ley en `kernel/laws`, ninguna omitida:
   - Conservación: un intercambio cuyos asientos no suman cero por recurso es rechazado al confirmar.
   - Memoria: `UPDATE` y `DELETE` sobre eventos, asientos y cumplimientos fallan. Una corrección se hace con un intercambio compensatorio.
   - Compromiso: sin `due_at` o `status`, o con `fulfilled_amount > amount`, se rechaza.
   - Permiso: una operación sin actor o sin permiso es rechazada y no escribe nada.
   - Movimiento: crear o reprogramar un compromiso con vencimiento deja su timer en la misma transacción. Con el reloj adelantado, el timer se dispara y el compromiso queda `expired`.
2. **Una transacción.** Un intercambio y un compromiso se escriben juntos con su evento y su fila de outbox. Si falla cualquiera de las escrituras, no queda ninguna.
3. **Aislamiento.** Con dos espacios, ninguna lectura ni escritura del espacio A alcanza filas del B, en cada tabla de datos de espacio de M1. Una conexión sin `space_id` no ve ninguna fila. El rol de la API no tiene `BYPASSRLS` ni es dueño de las tablas.
4. **Idempotencia.** Repetir una operación de dinero con la misma clave y el mismo cuerpo devuelve la misma respuesta sin duplicar asientos. Con otro cuerpo devuelve un error tipado y no escribe.
5. **Outbox.** Cada evento deja su fila `pending` en la misma transacción y puede reclamarse sin que dos reclamantes obtengan la misma fila.
6. **Migraciones.** Una base vacía llega al esquema `kernel` completo de M1 con una sola orden, en local y en CI.
7. **Pureza del kernel.** La prueba de arquitectura sigue en verde, y las pruebas de M1 usan tipos neutros, sin vocabulario de negocio.
8. **Calidad de proceso.** `task lint`, `task test` y `task docs:check` en verde; ADR de migraciones aceptado; docs actualizados.

## Supuestos y riesgos de producto

- **Supuesto:** el modelo de `docs/02-arquitectura/modelo-de-datos/kernel.md` es la fuente de verdad del esquema, recortado a las tablas de M1. Si una tabla recortada es requisito de otra de M1 (por ejemplo una clave foránea), se resuelve en la historia, no ampliando el alcance.
- **Supuesto:** los eventos de M1 se validan en Go sin JSON Schema publicado. El modelo de datos pide `payload` validado con JSON Schema, así que queda una deuda controlada hasta B3, cuando se crean los contratos.
- **Nota de viabilidad sobre RLS:** `SET LOCAL` por transacción funciona con el pooler en modo sesión de ADR-0019. Es viable. La tensión es que el acceso privilegiado del staff (B4) necesitará una vía controlada que M1 no abre. Debe dejarse documentado que esa vía es de B4 y que no se resuelve debilitando la RLS.
- **Nota de viabilidad sobre el reloj:** probar la ley de movimiento con reloj inyectable a nivel de paquete es viable. Sin el proceso programador real, nada dispara timers en producción. Es aceptable mientras no haya espacios reales, y debe quedar dicho.
- **Riesgo, tamaño:** XL con una sola prueba de cierre. Mitigación: historias finas y las leyes verificadas desde la primera historia que toca cada tabla, no al final.
- **Riesgo, divergencia documental:** `hoja-de-ruta.md` ubica el aislamiento en B6 y `testing.md` pide Testcontainers. Si no se actualizan en el mismo cambio, la documentación contradice el código.
- **Riesgo, RLS mal configurada:** si el rol de la API es dueño de las tablas o tiene `BYPASSRLS`, la RLS no protege. Se cubre con el criterio 3.
- **Riesgo, vocabulario:** la prueba de arquitectura revisa identificadores Go, no SQL, cadenas ni comentarios. Los ejemplos de `kind` en pruebas deben ser neutros por disciplina de revisión.

## Decisiones que requieren al usuario

Las decisiones ya tomadas por el responsable del proyecto no se reabren. Quedan estas, que cambian el alcance o el orden y no están resueltas:

1. **Catálogo mínimo de permisos del kernel.** El motor de capacidades necesita claves de permiso concretas. Propuesta: el Analista de Requisitos define el catálogo mínimo (solo lo que usan las operaciones de M1) y el responsable lo aprueba. Confirmar.
2. **Proceso programador de timers.** La propuesta es que M1 pruebe el disparo con reloj inyectable a nivel de paquete y que el proceso en `services/api` (con `scheduler_runs`) se construya cuando haga falta en M4 o antes. Confirmar, o moverlo a M1 (subiría el esfuerzo).
3. **Rama base y nombres de historias.** El repositorio está en `chore/HU-urls-adr-01`, no en `sprint/S01`. Confirmar la rama de integración y el prefijo de las historias (`feature/HU-kernel-<tema>-NN`).
4. **Tablas de soporte restantes.** `contact_points`, `invitations`, `usage_counters` y `access_audit` no están asignadas. Propuesta: M1 incluye solo las que usa alguna operación de M1 (se espera que `access_audit` se cree junto con los grants porque la regla 10 la exige para el acceso privilegiado, aunque su uso real sea B4). Las demás esperan a su fase. Confirmar.
