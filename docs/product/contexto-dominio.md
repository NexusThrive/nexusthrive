# Contexto de dominio: NexusThrive, fase M2 (B3), las puertas del kernel

Rama: `sprint/S02`. Este brief reemplaza al de M1 y conserva lo que sigue vigente (propósito, glosario, restricciones). Todo lo que aquí se afirma como actual sale de código o documentos citados; lo que no está escrito va a "Preguntas abiertas".

## Propósito del producto

NexusThrive es un kernel que estructura ecosistemas y los mantiene funcionando. No conoce ningún negocio: siete primitivas (`Participant`, `Resource`, `Exchange`, `Commitment`, `Event`, `Timer`, `Grant`/`Capability`) y cinco leyes (conservación, memoria, compromiso, permiso, movimiento) (`AGENTS.md`, `docs/01-modelo/primitivas.md`, `docs/01-modelo/leyes.md`). Tres capas lo rodean: plataforma (`platform/`, `apps/console`), distribuciones (`distributions/`, la primera es Thrive Business) y apps sin lógica (`AGENTS.md`).

**Esta petición: M2 = B3.** Hoja de ruta (`docs/02-arquitectura/hoja-de-ruta.md`): M2 es "Puertas completas y staging en línea". Criterio de terminado de B3: "Una operación entra por la API con token de Clerk, se ejecuta, se repite con la misma clave sin duplicarse y su señal llega a un webhook firmado". Una fase no termina sin ese criterio probado automáticamente.

Alcance MVP de B3 (`docs/05-decisiones/0018-recorte-de-la-etapa-1.md`):

- **Entra:** API Connect-RPC y JSON/HTTP, Clerk, idempotencia, credenciales con alcances, MCP, webhooks firmados, nodo de n8n generado.
- **Pospuesto (no se construye sin ADR nuevo):** MCP con recursos y prompts avanzados, SDK en otros lenguajes.

La petición incluye además la integración n8n y el despliegue a staging (ADR-0019, `docs/02-arquitectura/despliegue.md`).

## Usuarios y roles visibles en el código

- **Tipos de actor** (`kernel/model/space.go`, `ActorKind`): `person`, `agent`, `api_key`, `system`. `Actor{Kind, ParticipantID}`; solo `system` puede ir sin participante.
- **Proveedor de identidad** (`kernel/model/participant.go`): `clerk`, `api_key`, `system`.
- **Roles estándar por espacio:** `owner` y otros definidos en `kernel/model/permission.go` y sembrados por `kernel/state/standard_roles.go`.
- **Catálogo de permisos del kernel** (`kernel/model/permission.go`): `kernel.participant.read|write`, `kernel.role.write`, `kernel.grant.write`, `kernel.capability.write` (solo espacios `platform`), `kernel.resource.write`, `kernel.ledger.read`, `kernel.exchange.write`, `kernel.commitment.read|write`, `kernel.event.read`, `kernel.timer.process`, `kernel.outbox.claim`.
- **Autenticación según docs** (`docs/04-integraciones/identidad.md`): persona de negocio con Clerk; equipo NexusThrive con Clerk y doble factor (B4/B6); agentes e integraciones con llave (`kernel.api_keys`, alcances más `grants` de su participante). Regla: "Clerk solo prueba quién eres; el kernel decide qué puedes hacer" (ADR-0006).
- **Usuarios de n8n y MCP:** n8n usa la API; el agente MCP es un participante con identidad propia (`docs/04-integraciones/puertas.md`).

## Glosario del dominio (término -> dónde aparece)

| Término | Significado | Dónde aparece |
|---|---|---|
| `Space` | Unidad de aislamiento (`platform`, `business`, `community`, `personal`); estados `provisioning`, `active`, `suspended`, `closed` | `kernel/model/space.go`, migración `00003_spaces.sql` |
| `Identity` | Quien puede iniciar sesión, única por `(provider, subject)` | `kernel/state/identity.go`, ADR-0006 |
| `Participant` | Una identidad (o alguien sin cuenta) dentro de un espacio | `kernel/state/participant.go` |
| `Actor` | Quién ejecuta una operación; hoy lo afirma la aplicación | `kernel/model/space.go`, ADR-0035 |
| `Operation` | Declaración en Go: nombre, permisos, capacidad, riesgo, idempotencia | `kernel/execution/operation.go` |
| `Engine` | Motor: una operación, una transacción | `kernel/execution/engine.go` |
| `Call` | Actor, espacio, `RequestID`, `IdempotencyKey`, `Request` (cuerpo recibido) | `kernel/execution/engine.go` |
| `Tx` | Transacción mínima sin SQL: `RecordExchange`, `CreateCommitment`, `AppendEvent`, `ReadEvents` | `kernel/execution/tx.go` |
| `Authorizer`, `Check` | Verificación de permiso y capacidad dentro de la transacción | `kernel/capability/authorizer.go`, ADR-0032 |
| `Event`, `Outbox`, `Subscriber` | Memoria de solo agregar y entrega pendiente; suscriptores `webhooks`, `n8n`, `ai`, `internal` | `kernel/model/outbox.go`, migraciones 00006 y 00010, ADR-0028, ADR-0031 |
| `idempotency_records` | Respuesta guardada 24 h por (espacio, clave); huella = operación, actor y cuerpo canónico | migración 00014, ADR-0026, `kernel/model/idempotency.go` |
| Códigos de error | `invalid_input`, `rule_violation`, `not_found`, `permission_denied`, `already_done`, `space_suspended`, `capability_inactive`, `idempotency_conflict` | `kernel/model/errors.go`, `docs/02-arquitectura/contrato.md` |
| `PanicError` | Pánico recuperado, sin código tipado | `kernel/execution/errors.go`, ADR-0035 |
| `ApiKey`, `WebhookEndpoint`, `WebhookDelivery`, `Notice`, `Invitation` | Solo en el modelo documentado; sin migración | `docs/02-arquitectura/modelo-de-datos/kernel.md` |
| Puertas | API, MCP, webhooks salientes y entrantes | `docs/04-integraciones/puertas.md` |

## Módulos relacionados con la solicitud

| Ruta | Estado verificado |
|---|---|
| `kernel/gateway` | **No existe.** Aparece solo en `kernel/README.md` y `docs/02-arquitectura/kernel.md` como paquete planeado ("Connect API, MCP and webhook endpoints") |
| `kernel/execution` | Existe: `Engine` (`Run`, `RunIdempotent`, `FireDueTimers`, `ClaimOutbox`), `Operation`, `Tx`, `PanicError` |
| `kernel/capability` | Existe: `Authorizer` (`Check`, `CheckRoleCreatable`, `CheckGrantable`, `CheckRevocable`, `CheckCapabilityEnable`) |
| `kernel/state` | Existe; solo `kernel/state` y `kernel/execution` pueden importarlo (`kernel/architecture_test.go`). Las puertas no pueden importarlo |
| `kernel/model` | Tipos, errores, catálogo de permisos, `RequestHash` |
| `kernel/db` | 14 migraciones, comando `kernel/db/cmd/migrate`, arnés `dbtest` |
| `kernel/laws` | Pruebas de las cinco leyes, aislamiento por catálogo, guardia contra `t.Skip` |
| `kernel/coordination`, `kernel/extension` | No existen (ADR-0035 pospone `extension`) |
| `contracts/` | Solo `buf.yaml`, `buf.gen.yaml` y READMEs. No hay `.proto` ni JSON Schema. `buf.gen.yaml` apunta a `kernel/gen` (no existe) y `packages/sdk/src/gen` |
| `packages/sdk` | Existe el directorio de paquete; el SDK generado no existe (no hay contrato) |
| `services/api` | `cmd/api/main.go` con `main()` vacío (comentario "Wiring is implemented in phase F1") |
| `platform/`, `distributions/business/` | Solo `doc.go` y `go.mod` |
| `integrations/n8n/nodes`, `integrations/n8n/workflows` | Solo README |
| `infra/docker/compose.yaml` | PostgreSQL y Redis locales; sin infraestructura de staging |
| `.github/workflows/ci.yml` | Único flujo; no hay flujo de despliegue |

## Comportamiento actual relevante (solo lo verificado en el código)

### Estado del kernel tras M1

- M1 (B2) está completo: 15 historias, 15 terminadas, PR #26 fusionado (`docs/product/plan.md`, `docs/ingenieria/sprint-01/reporte.md`; `git log` en `main`).
- Tablas del esquema `kernel` migradas: `identities`, `participants`, `spaces`, `resources`, `accounts`, `roles`, `grants`, `space_capabilities`, `access_audit`, `events`, `outbox`, `exchanges`, `entries`, `commitments`, `fulfillments`, `timers`, `idempotency_records` (`kernel/db/migrations/00001` a `00014`).
- **No existen** las tablas `api_keys`, `invitations`, `contact_points`, `webhook_endpoints`, `webhook_deliveries`, `notices`, `scheduler_runs`, `usage_counters`, `files` (ninguna migración las crea; solo están en `docs/02-arquitectura/modelo-de-datos/kernel.md`). Cualquier puerta que las necesite exige migración nueva y, por la regla 12, ADR previo.
- Las cinco leyes tienen pruebas en `kernel/laws`; el aislamiento se prueba sobre todas las tablas con `space_id` leídas del catálogo de PostgreSQL; el rol de la API no tiene `SUPERUSER`, `BYPASSRLS` ni `LOGIN` (`docs/ingenieria/sprint-01/reporte.md`).

### Paquetes que B3 consumirá

- **`execution.Engine`.** `NewEngine(pool, auth, ops...)` valida todas las operaciones al arrancar (`kernel/execution/engine.go`). Solo hay dos operaciones registradas por el propio kernel, `kernel.timer.fire` y `kernel.outbox.claim` (`kernel/execution/builtin.go`). **Ninguna operación no-kernel está declarada fuera de pruebas** (búsqueda de `execution.Operation{` en código no de prueba: sin resultados). Es decir, hoy la API no tendría qué exponer: hace falta decidir y declarar la primera operación (p. ej. con permisos `kernel.exchange.write` o `kernel.event.read`).
- `Run` rechaza con `invalid_input` todo nombre que empiece por `kernel` y toda operación `Idempotent`; las operaciones idempotentes (obligatorio para `money`) solo corren por `RunIdempotent` (`kernel/execution/engine.go`, ADR-0035, ADR-0026).
- `Call` documenta que el motor confía en el actor y que quien lo construye debe haberlo verificado; `Call.Request` "lo llenan las puertas de B3" (`kernel/execution/engine.go`).
- **Resolución token a participante:** no existe función que, dado `(provider, subject)` de una identidad y un espacio, devuelva el participante. `kernel/state/identity.go` y `participant.go` solo exponen creación y archivado. Falta esa pieza para construir el `Actor` (verificado por búsqueda en `kernel/`). Además `state` no es importable por una puerta, así que debe pasar por `execution` u otro paquete permitido: decisión de diseño abierta.
- **`capability.Authorizer.Check`** aplica, en orden, validaciones de espacio, participante activo, grant vigente, suspensión y capacidad; códigos `permission_denied` (mensaje único), `space_suspended`, `capability_inactive` (`docs/02-arquitectura/kernel.md`, ADR-0032).
- **Errores:** `PanicError` no tiene código; B3 debe traducirlo a respuesta genérica sin mensaje ni pila. No hay código reintentable en el contrato; `40001` y `40P01` salen sin tipo (ADR-0035, seguimiento 33).
- **Outbox y entrega:** el outbox admite entrega al menos una vez, con reclamo y arrendamiento (ADR-0031); el consumidor debe deduplicar por `event.id` (seguimiento 39). Los eventos de `kernel.commitment.expired` llegan solo a `internal` hasta que B3 decida el despacho (seguimiento 64).
- **Timers:** `Engine.FireDueTimers` existe y está probado con reloj inyectable, pero ningún proceso lo invoca; no hay `kernel/coordination` ni `scheduler_runs` (`kernel/README.md`, seguimientos 60 y 61).

### Contratos en `contracts/`

- `contracts/buf.yaml` y `contracts/buf.gen.yaml` configurados. Plugins: Go y Connect Go a `kernel/gen`, `bufbuild/es` a `packages/sdk/src/gen` (sin generación para Python en el archivo, aunque `docs/02-arquitectura/contrato.md` la menciona).
- `contracts/proto/` y `contracts/events/` solo tienen README: no hay un solo `.proto` ni JSON Schema. `task gen` y `lint:contracts` no hacen nada mientras no haya `.proto` (según el brief de M1; el `Taskfile.yml` define `gen` y `lint:contracts`).
- Reglas documentadas: cada operación declara permiso, capacidad, riesgo e idempotencia; paquetes `kernel.v1`, `platform.v1`, `business.v1`; eventos con nombre `<dominio>.<sujeto>.<verbo>.v<N>.json`; `buf lint` y `buf breaking` en el CI (`contracts/README.md`, `docs/02-arquitectura/contrato.md`).
- ADR-0020: de cada operación se generan método de API, SDK, herramienta MCP, evento (JSON Schema) y operación del nodo n8n, con una prueba de arquitectura que falla si una operación existe en una puerta y no en otra.

### Límites aceptados en M1 (ADR-0035 y reporte)

1. El actor lo afirma la capa de aplicación; el kernel comprueba que el participante tenga permiso, no quién llama (ADR-0032, ADR-0035; seguimientos 28, 29, 46).
2. La ruta alternativa por el pool existe: quien tenga el pool con el rol de la API puede saltarse `Check`. Mitigación: la `Tx` no expone SQL, el pool del motor no se exporta y una prueba de arquitectura limita los importadores de `kernel/state`.
3. Nada dispara timers en producción.
4. Quien tenga el rol dueño o un superusuario puede deshabilitar disparadores con DDL (auditoría de DDL en B6).
5. Las pruebas usan `SET ROLE`, no un inicio de sesión real con el rol limitado (seguimientos 2 y 71).
6. Sin reintentos ante conflictos de serialización.

**Precondiciones de B3 (ADR-0035):**

- Las puertas y extensiones reciben solo el `Engine`, nunca el pool ni `kernel/state`.
- El `Actor` se construye solo desde un token validado; `ActorSystem` nunca desde una petición.
- No se exponen `ClaimOutbox`, `MarkOutboxSent` ni `MarkOutboxFailed` a clientes.
- `*PanicError` se traduce a un error genérico sin mensaje ni pila.
- Un `Engine` por proceso del kernel.

### Seguimientos de M1 destinados a B3

Fuente: `docs/ingenieria/sprint-01/seguimientos.md` (números de esa tabla).

| # | Qué debe resolver B3 |
|---|---|
| 4 | `IdentityID` nunca viene de la petición de un cliente; se resuelve en el servidor |
| 6 | Vía para buscar o crear identidad sin enumeración, con límite de tasa (ADR propio) |
| 12 | Tamaño máximo del `payload` de eventos, en los contratos de eventos |
| 13 | Consumo por cursor de `seq`: reanudar con traslape y deduplicar por `id` (ADR propio) |
| 28, 29, 46 | Actor certificado desde el token; certificar al actor de plataforma |
| 33 | Código reintentable en el contrato (`40001`) |
| 39 | Despacho del outbox entre espacios (lista de espacios activos, rotación, equidad), consumidor que deduplica por `event.id`; corregir "hasta 8" reintentos de `flujos.md` |
| 41 | Revisar si B3 cambia el reloj |
| 42 | No exponer `ClaimOutbox` ni sus marcadores |
| 47 | Reactivar un espacio `suspended` exige actor certificado (B3 y B4) |
| 64 | Decidir despacho de `kernel.commitment.expired` a webhooks, n8n e IA |
| 69 | La puerta pasa el cuerpo recibido sin reescribirlo y la clave de idempotencia (cabecera o campo) en `Call.Request` y `IdempotencyKey` |
| 70 | El SDK debe serializar siempre igual (`1` y `1.0` son cuerpos distintos; sin normalización Unicode); etiqueta `nexusthrive.idempotency.v1` |
| 72 | El `ctx` de la puerta acota la espera de la segunda solicitud con la misma clave (tiempo máximo y posible límite por clave) |

Antes del despliegue de staging (M2): seguimiento 1 (contraseña `API_DB_PASSWORD` del rol de la API sin dejarla en logs; el ADR-0027 propone calcular el verificador SCRAM en Go; una variable vacía no revoca un LOGIN ya concedido) y 2 (probar inicio de sesión real con el rol limitado). Pulido de M2: 76 (bases `kernel_test_*` acumuladas). Para B4/B6 (no B3): 21, 43, 47, 10, 63, 68.

Decisiones pendientes con ADR que pueden tocar a B3: 35 (compensar una compensación), 56 (autocumplimiento), 63 y 68 (retención). Están anotadas como "Después de M1" en `docs/05-decisiones/README.md`.

### Lo que dice la documentación sobre cada puerta

- **API** (`docs/04-integraciones/puertas.md`): token de Clerk o llave por espacio, alcances, límites por minuto, idempotencia. Connect-RPC y JSON/HTTP en `api.nexusthrive.com.co` (ADR-0023). Límites de tasa en memoria, sin Redis en production (ADR-0025).
- **MCP:** `puertas.md` y `AGENTS.md` dicen "solo lectura al principio"; ADR-0018 y ADR-0020 dicen "lectura y escritura controlada" con propuestas aprobadas por una persona. Resuelto por S-P1 y [ADR-0044](../05-decisiones/0044-mcp-de-solo-lectura-en-m2.md): solo lectura en M2, escritura pospuesta. Ruta `/mcp` en la misma API (ADR-0023).
- **Webhooks salientes:** firmados con HMAC, reintentos con espera creciente, registro de entregas, reenvío manual y rotación del secreto; un endpoint se deshabilita tras 20 fallos seguidos (`puertas.md`, ADR-0020). Cada evento lleva identificador estable para deduplicar.
- **Webhooks entrantes:** firma verificada y protección contra repetición (`docs/07-ingenieria/security.md`, punto 10). `/webhooks/wompi` pertenece a B4 (cobro, ADR-0022), no a B3.
- **n8n:** nodo generado desde el contrato con trigger por eventos y plantillas de flujos; si n8n falla, solo se atrasan avisos y nunca guarda ni mueve valor (ADR-0005, ADR-0020). `integrations/n8n/` está vacío. La licencia comercial de n8n para credenciales de clientes está pendiente y bloquea la fase N3, no B3 (`docs/05-decisiones/README.md`).
- **Clerk:** no hay en el repo código de validación de tokens, JWKS ni dependencia de Clerk. Solo existe la mención en `development-setup.md` ("completar las llaves de Clerk") y `.env.example` por verificar.

## Qué se puede construir y probar sin cuentas externas, y qué las exige

Esta separación es un análisis a partir de lo documentado; no está escrita como tal en el repositorio.

### Sin cuentas externas (PostgreSQL local de `task infra:up` y pruebas)

| Pieza | Cómo se prueba sin cuenta |
|---|---|
| Contratos: primeros `.proto` de `kernel.v1`, JSON Schema de eventos, `buf lint`, `buf breaking`, `task gen` | `buf` con plugins remotos requiere red a `buf.build` (plugins `remote:` en `buf.gen.yaml`); verificar si el entorno de CI lo permite o usar plugins locales (pregunta abierta) |
| Declarar la primera operación del kernel y registrarla en el `Engine` | Pruebas contra PostgreSQL real, como en M1 |
| Paquete `kernel/gateway`: recibe solo el `Engine`, construye `Call`, traduce errores tipados a códigos Connect, traduce `PanicError` a error genérico, acota `ctx` | Pruebas con `httptest` y base local; prueba de arquitectura que prohíbe importar `kernel/state` y el pool |
| **Validación de tokens de Clerk** (JWT: firma, `iss`, `exp`, `nbf`, `azp`, algoritmo permitido) | JWKS simulado con un servidor `httptest` y claves RSA/EC generadas por la prueba; casos: token vencido, firma ajena, `kid` desconocido, `alg=none`, rotación de claves, caída del JWKS (caché y plazo). Solo la prueba de humo contra la instancia real exige cuenta |
| Resolución token a participante y construcción del `Actor` | Base local con identidades y participantes sembrados |
| Llaves por espacio con alcances (`api_keys`: migración, hash, vista única, revocación, rotación) | Base local; requiere ADR y migración nueva |
| Idempotencia en la puerta (cabecera a `Call.IdempotencyKey`, cuerpo a `Call.Request`), seguimientos 69 a 72 | Base local, con concurrencia |
| MCP de lectura | Cliente MCP de prueba en proceso, sin servicios externos |
| Webhooks salientes: firma HMAC, reintentos con espera creciente, deshabilitar tras 20 fallos, `webhook_endpoints` y `webhook_deliveries`, despacho del outbox | Receptor `httptest`, reloj inyectable; requiere migraciones y ADR de despacho (seguimientos 13, 39) |
| Criterio de B3 de extremo a extremo: token (JWKS simulado), operación, repetición con la misma clave, evento a webhook firmado | Todo local |
| Nodo n8n generado y su paquete (`integrations/n8n/nodes`) | Pruebas unitarias del nodo y del trigger contra un API local; una instancia n8n real no es necesaria para generar ni probar el paquete |
| Límites de tasa en memoria (ADR-0025) | Pruebas con reloj inyectable |
| Imagen Docker de `services/api`, `main.go` con el cableado, migraciones al arrancar, comprobación de salud | `docker build` y `task infra:up` local |
| Contraseña del rol de la API calculada como verificador SCRAM sin dejarla en logs (seguimiento 1) | Pruebas locales; el inicio de sesión real con el rol limitado también puede ejercitarse contra el PostgreSQL local |

### Exigen cuentas o accesos externos

| Pieza | Qué exige (fuente) |
|---|---|
| Prueba de humo con un token real de Clerk y el JWKS real | Cuenta de Clerk con claves de desarrollo (reporte, "Lo que necesita el responsable", punto 4) |
| Base de staging | Proyecto de Supabase de staging, pooler en modo sesión, Data API y Auth desactivados (ADR-0019) |
| Despliegue de la API a staging | Acceso al servidor con Dokploy; despliegue automático desde `main`; migraciones antes de recibir tráfico (ADR-0019) |
| URL pública `staging-api.nexusthrive.com.co` y certificados | DNS de `nexusthrive.com.co` (ADR-0023, ADR-0019) |
| Web y consola de staging (`staging.`, `staging-console.`) | Vercel y decisión sobre su plan para uso comercial; no son parte de B3 |
| Flujo de despliegue en GitHub Actions, secretos por entorno | Acceso a los secretos del proveedor ("los secretos viven en el gestor del proveedor", ADR-0019) |
| n8n de staging y credenciales separadas por entorno | Servidor propio con Dokploy (ADR-0019) |
| Redis de staging (solo para probar el camino con Redis) | Dokploy (ADR-0025); no es necesario para la corrección |
| Inicio de sesión real del rol limitado contra Supabase | Base de staging (seguimiento 2) |
| Producción (Render, Supabase de pago) | Fuera de M2 |

**Consecuencia de separación en historias:** el grueso de B3 (contratos, gateway, validación JWT con JWKS simulado, llaves, idempotencia en la puerta, MCP de lectura, webhooks firmados, nodo n8n, imagen y cableado) se puede entregar y probar sin ninguna cuenta. Solo las historias de despliegue y la prueba de humo con Clerk real dependen de las cuentas. El criterio de terminado de B3 se puede demostrar localmente con JWKS simulado; la parte "staging en línea" de M2 queda bloqueada por las cuentas.

## Restricciones técnicas

- **Stack:** Go 1.24, `go.work` (`kernel`, `platform`, `services/api`, `distributions/business`), `sqlc` y `pgx`, Connect-RPC, errores tipados, `fmt.Errorf("...: %w", err)`. TypeScript estricto, pnpm, Turborepo (`AGENTS.md`).
- **Reglas de arquitectura:** el kernel no aprende palabras de negocio; no hay importaciones cruzadas; solo el kernel y sus extensiones tocan la base; una operación, una transacción; toda operación declara permiso, capacidad, riesgo e idempotencia; las operaciones de dinero son siempre idempotentes; solo se construye lo que exige la fase; un cambio significativo requiere ADR antes del código (`AGENTS.md`, reglas 1 a 12).
- **Importación:** la prueba de arquitectura (`kernel/architecture_test.go`) impide que algo fuera de `kernel/state` y `kernel/execution` importe `kernel/state`. `kernel/gateway` queda por tanto sin acceso a `state`.
- **Base de datos:** PostgreSQL 17 local; Supabase solo como PostgreSQL administrado; pooler solo en modo sesión (`pgx` usa sentencias preparadas); las migraciones corren antes de recibir tráfico; un proyecto por entorno (ADR-0019). La numeración de migraciones continúa en `00015`.
- **Roles de base:** el rol `nexusthrive_api` es el proceso del kernel; no es dueño de objetos y no tiene `LOGIN` salvo que se aplique contraseña (ADR-0027, seguimientos 1, 9, 25).
- **Entornos y dominios:** `local`, `staging`, `production`. Staging: API en servidor propio con Dokploy desde `main`; subdominios `staging`, `staging-api`, `staging-console`; production con Render por etiqueta (ADR-0019, ADR-0023, `docs/07-ingenieria/ci-cd.md`).
- **Redis:** no es parte de la corrección; límites de tasa en memoria (ADR-0025).
- **Datos:** dinero con `numeric`; UUID v7; `timestamptz` en UTC; locale `es-CO`, zona `America/Bogota`, moneda `COP`.
- **Idioma:** `docs/` en español; código, comentarios, logs, errores internos, pruebas, commits y PR en inglés (`docs/07-ingenieria/language-policy.md`).
- **Git:** ramas `feature|fix|chore|hotfix/HU-<slug>-NN` hacia `sprint/S02`; commits Conventional Commits; un PR por historia con `docs/` actualizado; antes del PR `task lint`, `task test`, `task docs:check` (`AGENTS.md`).
- **Seguridad:** secretos nunca en el repositorio; acceso privilegiado temporal y auditado; webhooks entrantes con firma y protección contra repetición (`docs/07-ingenieria/security.md`).
- **Pruebas:** prueba antes que implementación; `kernel/laws` nunca se omite; pruebas de aislamiento e idempotencia para toda operación de dinero (`docs/07-ingenieria/testing.md`).
- **Árbol de trabajo:** `go.work.sum` aparece sin seguimiento (seguimiento 79).

## Preguntas abiertas para el equipo

1. **MCP: solo lectura o lectura y escritura controlada.** `AGENTS.md`, `puertas.md` y la hoja de ruta (la petición dice "MCP de solo lectura") frente a ADR-0018 y ADR-0020 ("lectura y escritura controlada", propuestas aprobadas por una persona). ¿Qué fija M2? Resuelto: S-P1 confirmado y [ADR-0044](../05-decisiones/0044-mcp-de-solo-lectura-en-m2.md); solo lectura en M2 y escritura pospuesta.
2. **Primera operación del contrato.** No hay ninguna fuera de `kernel.timer.fire` y `kernel.outbox.claim`, que no se exponen. ¿Qué operación de `kernel.v1` demuestra el criterio de B3 (por ejemplo un intercambio con compromiso)? ¿Es de dinero (idempotente obligatoria)? No hay documentación de la lista de operaciones del kernel.
3. **Resolución token a participante.** ¿Dónde vive (en `execution`, en un paquete nuevo con permiso para importar `state`, o una operación del motor)? ¿Cómo se elige el espacio en una petición: ruta con `slug` (ADR-0023), cabecera o campo del contrato? Hoy no hay documento que fije cómo la API resuelve el espacio.
4. **Identidad del primer ingreso.** Alta de la identidad de Clerk la primera vez que llega un token (seguimiento 6): ¿invitación verificada, creación por `system` o por webhook de Clerk? Hace falta ADR; no está documentado.
5. **Alcance de `kernel/extension`.** ADR-0035 no lo crea; las puertas reciben un `Engine`, pero no se dice dónde se compone el conjunto de operaciones al arrancar `services/api`.
6. **Qué token acepta la API.** Qué claims y qué audiencia valida el kernel (`iss`, `aud`, `azp`), qué plazo de caché del JWKS y qué hacer ante la caída del emisor. No está en los documentos.
7. **Llaves por espacio.** `api_keys` está en el modelo pero sin migración. ¿Entran en M2 con el MCP y n8n (que dependen de llaves según `identidad.md`)? Formato del secreto, prefijo, `environment` (`live`, `test`), `ip_allowlist` y alcances: hay columnas en el diagrama pero sin ADR.
8. **Despacho del outbox.** Quién lo ejecuta (¿`kernel/coordination` y el proceso programador, que seguimientos 60 y 61 ubican en "M4 o antes"?), con qué política entre espacios y cuántos reintentos (el seguimiento 39 dice que `flujos.md` dice "hasta 8" y que B3 lo corrige; ADR-0031 fija el quinto fallo a `dead`; `puertas.md` fija 20 fallos para deshabilitar un endpoint). Hay tres cifras que reconciliar.
9. **Webhooks: dónde se guarda el secreto.** El modelo guarda `secret_hash`, pero firmar HMAC exige conocer el secreto, no un hash. ¿Se cifra? Debe resolverse en ADR.
10. **Contratos y `buf`.** Los plugins de `buf.gen.yaml` son remotos (`buf.build`): ¿el CI y el entorno local pueden alcanzarlos? ¿Qué pasa con la generación para Python que `contrato.md` menciona y `buf.gen.yaml` no incluye?
11. **Qué entra de n8n en M2.** ¿Solo el nodo y el trigger generados, o también las plantillas de flujos versionadas de ADR-0020 (prueba por vencer, pago recibido, alerta de existencias bajas, que dependen de Wompi y Business)? Parte del texto de ADR-0020 sugiere que van después.
12. **Alcance de "staging en línea".** ¿Incluye web y consola en Vercel o solo la API y su base? ¿Redis de staging? El brief asume solo API, base, n8n y DNS.
13. **Proceso programador.** ¿`FireDueTimers` se arranca dentro del binario de la API en M2 o espera a M4 (seguimientos 60 y 61)? Hasta entonces los compromisos vencidos no expiran en staging.
14. **Seguimientos pendientes de decisión (35, 56, 63, 68).** ¿Se tratan en M2 o siguen "Después de M1"?
15. **Lecturas del responsable de M1 sin confirmar.** El reporte lista lecturas tomadas por el responsable en los PR de M1 (`spaces-03`, `grants-05`, `outbox-10`, `permissions-06`, `exchanges-08`, `commitments-11`, `timers-12`, `execution-13`, `idempotency-14`); su confirmación no consta en el repositorio.
16. **Pendiente de proceso.** El PR #1 (cerrado) tiene dos menciones a una herramienta en su descripción que `AGENTS.md` no admite; se dejó sin tocar a la espera de decisión (reporte, punto 4b).
