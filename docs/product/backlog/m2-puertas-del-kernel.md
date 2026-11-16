# Backlog: M2, las puertas del kernel (B3)

Funcionalidad: `docs/product/funcionalidades/m2-puertas-del-kernel.md`. Brief: `docs/product/contexto-dominio.md`. Prioridad: Must (ver la definición). Formato de referencia: `docs/product/backlog/nucleo-del-kernel.md`.

## Resumen

### Avance de HU-gateway-01 · 2026-11-15

Decisión aceptada por santy401 el 2026-11-15 en `chore/HU-gateway-01`: [ADR-0037, contrato inicial del kernel](../../05-decisiones/0037-contrato-inicial-del-kernel.md). Incluye escritura con compromiso, lectura de eventos, errores, generación local y acceso al espacio de nombres reservado mediante métodos fijos del motor. No hay implementación de contratos ni cambios de código en esta historia. La integración y las comprobaciones completas siguen pendientes; HU-gateway-08 ya tiene su decisión aceptada.

El ADR-0036 ya pertenece a la primera puerta HTTP y no se reutiliza. Los números 0036 a 0044 citados abajo eran reservas originales, no decisiones existentes: HU-gateway-01 usa ahora 0037; los demás ADR se numerarán al redactarlos, sin colisionar con los existentes. Las dependencias por ID de historia se mantienen; las referencias numéricas originales deben resolverse por el tema de la historia hasta actualizar cada una.

- Historias: 23. Listas: 20. Bloqueadas: 3 (HU-gateway-22, HU-gateway-23 por cuentas externas; ver más abajo). Ninguna es XL.
- Tamaños: S = 5, M = 10, L = 8 (la rebanada R8, XL en la definición, se parte en HU-gateway-15, 16 y 17).
- Slug `gateway`. Ramas de tarea: `feature/HU-gateway-NN` (las historias solo de documentación usan `chore/HU-gateway-NN` si no llevan código; patrón `^(feature|fix|chore|hotfix)/HU-[a-z0-9]+(-[a-z0-9]+)*-[0-9]{2}$`). Rama base: `sprint/S02`. Un PR por historia, con `docs/` actualizado.
- Las historias 01 a 07 son de decisión (docs-only) y van primero. Ninguna historia de código empieza antes de que el ADR que exige tenga estado Aceptado (regla 12 de `AGENTS.md`). Los números de ADR (0036 en adelante; el último existente es 0035) son propuestos y los confirma el dueño al revisar el PR.

### Supuestos explícitos, por confirmar por el dueño

Las preguntas P2 a P11, P14 a P16 se resuelven con la decisión por defecto del Product Owner. Las tres que cambian el alcance se asumen así y se marcan con (*) en las historias afectadas:

- **S-P1 (*) MCP solo de lectura.** M2 expone `/mcp` únicamente con herramientas de operaciones `read`. La escritura con propuestas aprobadas por una persona queda pospuesta; ADR-0018 y ADR-0020 se enmiendan para quitar la contradicción con `AGENTS.md` y `puertas.md`. Afecta: 03, 14, 19.
- **S-P12 (*) Staging sin Vercel.** "Staging en línea" es solo API, base de staging, n8n de staging y DNS de la API. Sin web, consola, Vercel, Redis ni dominios `staging.` y `staging-console.`. Afecta: 23.
- **S-P13 (*) Programador en el binario de la API.** `FireDueTimers` y el despachador del outbox corren en el binario de la API con un ciclo simple, una sola instancia, sin `kernel/coordination` ni `scheduler_runs`. Si el dueño lo rechaza, 21 se pospone a M4 y en staging los compromisos vencidos no expiran. Afecta: 07, 15, 21.

Decisiones por defecto aplicadas sin marca (no cambian alcance): P2 primera operación `write` de `money` más una lectura de eventos; P3 y P5 resolución del actor en `execution` o paquete permitido por la prueba de arquitectura, composición en `services/api`, sin `kernel/extension`; P4 sin creación automática de identidad; P6 validación de Clerk con caché de JWKS; P7 llaves por espacio entran, `ip_allowlist` fuera; P8 tres cifras de reintento distintas; P9 secreto de webhook cifrado de forma reversible; P10 plugins de `buf` locales y fijados, Python fuera; P11 solo nodo y trigger de n8n; P14 y P15 fuera de M2; P16 decisión de proceso del dueño, fuera de B3.

### Orden y dependencias

```
01 ADR contrato -> 08 contratos -> 09 operaciones -> 10 gateway
02 ADR actor y espacio -> 12 actor
04 ADR Clerk -> 11 tokens -> 12 actor -> 10 gateway cableado con autenticación
05 ADR llaves -> 13 llaves
03 ADR MCP (*) -> 14 MCP (*)
06 ADR webhooks -> 15 -> 16 -> 17
07 ADR programador (*) -> 21 programador (*)
10 + 11 + 12 + 15 -> 18 criterio B3 de extremo a extremo
08 + 15 -> 19 nodo n8n
10 -> 20 imagen y salud
20 -> 22 humo con Clerk real (bloqueada), 23 staging (bloqueada) (*)
```

Orden sugerido: 01 a 07 (en paralelo), 08, 09, 10, 11, 12, luego 15, 18, 20, y en paralelo 13, 14, 16, 17, 19, 21; al final 22 y 23.

### Preguntas abiertas (solo las que bloquean)

| ID | Pregunta | Bloquea | Qué la desbloquea |
|---|---|---|---|
| Q1 | Credenciales de desarrollo de Clerk (instancia, `iss`, `azp`, clave pública) | HU-gateway-22 | El dueño entrega una instancia de desarrollo de Clerk |
| Q2 | Cuentas de Supabase (staging), servidor Dokploy y control del DNS de `nexusthrive.com.co` | HU-gateway-23 | El dueño crea las cuentas y entrega los accesos como secretos de GitHub |

Nota para el ADR de HU-gateway-01 (no bloquea): `validateOperation` reserva el espacio de nombres `kernel` a las operaciones del propio kernel (`kernel/execution/operation.go`) y `Run` rechaza todo nombre que empiece por `kernel` (`kernel/execution/engine.go`). El ADR debe fijar si `kernel.exchange.write` y `kernel.event.read` se registran por la ruta de operaciones propias del kernel y cómo las alcanza una puerta.

### Convenciones comunes a todas las historias

- **Pruebas primero** (`AGENTS.md`, Testing): el PR muestra pruebas fallando antes de la implementación. Pruebas de ley en `kernel/laws`, sin `t.Skip`.
- **Pureza** (`AGENTS.md` regla 1): la prueba `kernel/architecture_test.go` sigue en verde; sin vocabulario de negocio en `kernel/`.
- **Estilo**: código, identificadores, comentarios, pruebas y commits en inglés; `docs/` en español.
- **Cierre de PR**: `task lint`, `task test` y `task docs:check` en verde; `docs/` actualizado.
- **Regla de puerta** (aplica a toda historia que exponga una puerta: 10, 12, 13, 14, 15, 21): (a) la puerta recibe solo el `Engine`, nunca el pool de DB, y no importa `kernel/state` (prueba de arquitectura); (b) el `Actor` sale solo de un token o llave validados y `ActorSystem` jamás se construye desde una petición (ADR-0035); (c) el dinero (`risk = money`) exige clave de idempotencia y corre por `RunIdempotent` (ADR-0026, `kernel/execution/engine.go`). Cada una de estas historias la repite como criterio propio.
- **Datos**: dinero con `numeric`; IDs UUID v7; `timestamptz` en UTC.

---

# Parte 0: decisiones (solo documentación)

Todas son historias docs-only: el PR solo toca `docs/`, el ADR queda con estado Aceptado antes de mergear y `task docs:check` pasa. Tamaño S salvo que se indique.

## HU-gateway-01 · Decidir el contrato inicial de `kernel.v1`

- **Estado:** Terminada e integrada en `sprint/S02` el 2026-11-16. **Tamaño:** M. **Rama:** `chore/HU-gateway-01`. **ADR que escribe:** [0037](../../05-decisiones/0037-contrato-inicial-del-kernel.md) (contrato inicial de `kernel.v1`, herramienta `buf` y alcance de lenguajes; cubre P2 y P10).
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero fijar la lista inicial de operaciones de `kernel.v1` y la política de generación con `buf`,
  para que los contratos y las puertas se construyan sobre una decisión escrita.
- **Criterios de aceptación:**
  1. Dado `docs/05-decisiones/0037-contrato-inicial-del-kernel.md`, cuando se revisa, entonces tiene estado Aceptado y lista al menos una operación `write` de riesgo `money` (propuesta: `kernel.exchange.write`) y una `read` de eventos (propuesta: `kernel.event.read`), cada una con nombre, permiso del catálogo, riesgo, idempotencia, campos de entrada y salida y errores tipados.
  2. Dado el ADR, cuando se lee la operación `money`, entonces declara idempotencia obligatoria y la clave viaja en un campo del contrato.
  3. Dado el ADR, cuando se lee la sección de generación, entonces fija plugins de `buf` locales con versión fijada (no remotos), `buf lint` y `buf breaking` en CI, y excluye Python de M2.
  4. Dado el ADR, cuando se lee la sección de nombres, entonces resuelve cómo se registran las operaciones del espacio de nombres reservado `kernel` (nota de la cabecera) y cómo las alcanza el gateway sin saltarse el `Engine`.
  5. Dado `docs/02-arquitectura/contrato.md`, cuando se revisa, entonces ya no menciona Python como SDK de M2 y enlaza al ADR.
- **Reglas de negocio:**
  - Cambio significativo requiere ADR antes del código (`AGENTS.md` regla 12).
  - Toda operación declara permiso, capability, riesgo e idempotencia en `contracts/` (`AGENTS.md` regla 6); dinero siempre idempotente.
  - Permisos y riesgos salen del catálogo de `kernel/model` (ADR-0032).
  - Python y SDKs de otros lenguajes pospuestos (ADR-0018; Product Owner, P10).
- **Dependencias:** ninguna.
- **Fuera de alcance:** escribir `.proto` (HU-gateway-08), catálogo completo de operaciones, MCP de escritura.

## HU-gateway-02 · Decidir la resolución del actor, la selección del espacio y el primer ingreso

- **Estado:** Terminada e integrada en `sprint/S02` el 2026-11-16. **Tamaño:** M. **Rama:** `chore/HU-gateway-02`. **ADR que escribe:** [0038](../../05-decisiones/0038-resolucion-de-actor-y-espacio.md) (resolución de actor y espacio, composición en `services/api`; cubre P3 y P5) y [0039](../../05-decisiones/0039-identidad-en-el-primer-ingreso.md) (identidad en el primer ingreso; cubre P4).
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero decidir dónde se resuelve el token a participante, cómo se elige el espacio y qué pasa con un token sin participante,
  para cerrar los límites aceptados de ADR-0035 sin abrir una ruta por el pool.
- **Criterios de aceptación:**
  1. Dado el ADR 0038, cuando se revisa, entonces fija que el espacio viene en la ruta por `slug` (ADR-0023), que la resolución vive en `execution` o en un paquete que la prueba de arquitectura permita, nunca en la puerta, y qué excepción exacta a la regla de importación de `state` se concede (la propuesta conserva la regla sin excepciones nuevas).
  2. Dado el ADR 0038, cuando se revisa, entonces fija que `kernel/extension` no se crea en M2 y que la composición de operaciones vive en `services/api` y pasa solo el `Engine`.
  3. Dado el ADR 0039, cuando se revisa, entonces fija que no hay creación automática de identidad ni participante en M2 y que un token válido sin participante recibe `permission_denied` genérico, igual a un participante sin permiso, sin enumeración de espacios ni identidades.
  4. Dados ambos ADR, cuando se revisan, entonces tienen estado Aceptado y enlazan a ADR-0023, ADR-0035 y a los seguimientos 4 y 6.
- **Reglas de negocio:**
  - El actor lo afirma hoy la aplicación; B3 lo cierra (`docs/product/contexto-dominio.md`, límites aceptados 1; ADR-0035).
  - Las puertas reciben solo el `Engine` (ADR-0035).
  - Sin enumeración de identidades o espacios (Product Owner, P4).
- **Dependencias:** ninguna.
- **Fuera de alcance:** invitaciones, alta de identidades, implementación.

## HU-gateway-03 · Decidir el MCP de solo lectura (*)

- **Estado:** Terminada e integrada en `sprint/S02` el 2026-11-16. **Tamaño:** S. **Rama:** `chore/HU-gateway-03`. **ADR que escribe:** 0044 (enmienda a ADR-0018 y ADR-0020: MCP de solo lectura en M2).
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero resolver la contradicción entre los documentos sobre el alcance del MCP,
  para que el MCP y el nodo n8n se generen con el mismo alcance.
- **Criterios de aceptación:**
  1. Dado el ADR 0044, cuando se revisa, entonces establece que M2 expone solo herramientas generadas de operaciones `read`, y que la escritura con propuestas aprobadas por una persona queda pospuesta sin fecha de M2.
  2. Dado el ADR, cuando se revisa, entonces enmienda explícitamente ADR-0018 y ADR-0020 (nota de enmienda en ambos) y deja `AGENTS.md` y `docs/04-integraciones/puertas.md` coherentes, con la ruta `/mcp` de ADR-0023.
  3. Dado una búsqueda de "lectura y escritura controlada" en `docs/`, cuando se ejecuta, entonces cada aparición queda como pospuesta o enlaza a la enmienda.
- **Reglas de negocio:** MCP solo lectura en `AGENTS.md` y `puertas.md`; ADR-0018/0020 dicen lo contrario (brief, "Puertas"); decisión por defecto del Product Owner (P1).
- **Dependencias:** ninguna.
- **Fuera de alcance:** recursos y prompts avanzados, escritura por MCP.

## HU-gateway-04 · Decidir la validación de tokens de Clerk

- **Estado:** Terminada e integrada en `sprint/S02` el 2026-11-16. **Tamaño:** S. **Rama:** `chore/HU-gateway-04`. **ADR que escribe:** [0040](../../05-decisiones/0040-validacion-de-clerk-y-cache-jwks.md) (validación de tokens de Clerk y caché de JWKS; cubre P6).
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero fijar qué se valida de un token de Clerk y qué pasa si el emisor cae,
  para que la validación sea determinista y probable.
- **Criterios de aceptación:**
  1. Dado el ADR 0040, cuando se revisa, entonces exige validar firma, `iss`, `exp`, `nbf` y `azp` contra una lista permitida, solo algoritmos asimétricos y rechaza `alg=none`.
  2. Dado el ADR, cuando se revisa, entonces fija que un `kid` desconocido dispara una renovación del JWKS con límite de frecuencia numérico (el valor lo fija el ADR, no queda vago).
  3. Dado el ADR, cuando se revisa, entonces fija el plazo máximo en que se usa el JWKS en caché si el emisor no responde y, vencido el plazo, el rechazo.
  4. Dado el ADR, cuando se revisa, entonces tiene estado Aceptado.
- **Reglas de negocio:** validación de Clerk en el gateway (`AGENTS.md`, fase B3); decisión por defecto del Product Owner (P6). Los valores numéricos son nuevos y los fija el ADR.
- **Dependencias:** ninguna.
- **Fuera de alcance:** sesiones, MFA de staff (B6).

## HU-gateway-05 · Decidir las llaves por espacio

- **Estado:** Terminada e integrada en `sprint/S02` el 2026-11-16. **Tamaño:** S. **Rama:** `chore/HU-gateway-05`. **ADR que escribe:** 0045 (llaves de API por espacio, alcances y migración `api_keys`; cubre P7).
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero decidir el modelo de las llaves por espacio,
  para que n8n y los agentes de IA puedan autenticarse sin token de persona.
- **Criterios de aceptación:**
  1. Dado el ADR 0045, cuando se revisa, entonces fija: secreto visible una sola vez, solo hash en base de datos, prefijo identificable, `environment` con valores `live` y `test`, alcances como subconjunto de los permisos del participante, revocación y rotación.
  2. Dado el ADR, cuando se revisa, entonces declara `ip_allowlist` fuera de M2.
  3. Dado el ADR, cuando se revisa, entonces indica el número de migración (00018 reservado) y actualiza `docs/02-arquitectura/modelo-de-datos/kernel.md`.
- **Reglas de negocio:** modelo de datos y `puertas.md`; decisión por defecto del Product Owner (P7). `api_keys` no existe hoy (HU-kernel M1, decisión 4 del backlog de M1).
- **Dependencias:** ninguna.
- **Fuera de alcance:** `ip_allowlist`, implementación.

## HU-gateway-06 · Decidir despacho, reintentos y secreto de los webhooks

- **Estado:** Terminada e integrada en `sprint/S02` el 2026-11-16. **Tamaño:** M. **Rama:** `chore/HU-gateway-06`. **ADR que escribe:** 0041 (despacho del outbox y política de reintentos; cubre P8) y 0042 (secreto de webhooks; cubre P9).
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero fijar quién despacha el outbox, con qué política y cómo se guarda el secreto de firma,
  para que la entrega de webhooks sea consistente con ADR-0031.
- **Criterios de aceptación:**
  1. Dado el ADR 0041, cuando se revisa, entonces distingue por separado las tres cifras: el outbox pasa a `dead` al quinto fallo (ADR-0031), el endpoint se deshabilita tras 20 fallos seguidos (`puertas.md`) y la cifra de reintentos por entrega de `flujos.md` (hoy "hasta 8") se reemplaza por el valor que fije el ADR, con tabla de esperas crecientes.
  2. Dado el ADR 0041, cuando se revisa, entonces fija que el despachador corre dentro del binario de la API, rota entre espacios activos y deduplica por `event.id` (seguimientos 13 y 39), y define a qué suscriptores llegan los eventos `kernel.commitment.expired` (seguimiento 64).
  3. Dado el ADR 0042, cuando se revisa, entonces fija que el secreto se guarda cifrado de forma reversible con una clave de aplicación por entorno (no como hash), se muestra una vez y la rotación tiene una ventana con duración numérica donde valen ambos secretos.
  4. Dado `docs/02-arquitectura/modelo-de-datos/` y `flujos.md`, cuando se revisan, entonces `secret_hash` se corrige al campo cifrado y las cifras de reintentos de `flujos.md` coinciden con el ADR 0041.
- **Reglas de negocio:** ADR-0031 (quinto fallo a `dead`), `puertas.md` (20 fallos), seguimientos 13, 39, 64; decisión por defecto del Product Owner (P8, P9). Los valores de espera y de ventana son nuevos y los fija el ADR. El alcance del despachador depende de S-P13 (*): si no se acepta, el ADR indica quién lo ejecuta.
- **Dependencias:** ninguna.
- **Fuera de alcance:** implementación, `/webhooks/wompi` (B4).

## HU-gateway-07 · Decidir el proceso programador en el binario de la API (*)

- **Estado:** Terminada e integrada en `sprint/S02` el 2026-11-16. **Tamaño:** S. **Rama:** `chore/HU-gateway-07`. **ADR que escribe:** 0043 (proceso programador de M2; cubre P13, seguimientos 60 y 61).
- **Historia:**
  Como ingeniero de NexusThrive,
  quiero decidir cómo se ejecutan los timers vencidos y el despachador en M2,
  para que los compromisos expiren sin construir aún la coordinación multi-instancia.
- **Criterios de aceptación:**
  1. Dado el ADR 0043, cuando se revisa, entonces fija un ciclo simple dentro del binario de la API que invoca `FireDueTimers` y el despachador, con intervalo numérico, sin `kernel/coordination` ni `scheduler_runs`, y declara el límite de una sola instancia.
  2. Dado el ADR, cuando se revisa, entonces declara qué pasa si el dueño lo rechaza: se pospone a M4 y los compromisos vencidos no expiran en staging.
  3. Dado el ADR, cuando se revisa, entonces fija que el ciclo llama al kernel solo por el `Engine` con un actor `ActorSystem` construido por el propio proceso, no por una petición.
- **Reglas de negocio:** `kernel.timer.fire` y `kernel.outbox.claim` existen como operaciones propias (`kernel/execution/builtin.go`); `ActorSystem` solo se construye internamente (ADR-0035); decisión por defecto del Product Owner (P13).
- **Dependencias:** ninguna.
- **Fuera de alcance:** coordinación entre instancias, `scheduler_runs`.

---

# Parte A: construcción local (sin cuentas externas)

## HU-gateway-08 · Declarar los contratos de `kernel.v1` (R1)

- **Estado:** Terminada e integrada en `sprint/S02` el 2026-11-16. **Tamaño:** M. **Rama:** `feature/HU-gateway-08`. **ADR exigidos antes del código:** [0037, aceptado](../../05-decisiones/0037-contrato-inicial-del-kernel.md).
- **Avance 2026-11-15:** implementación local validada; pendientes revisión e integración. Pasan lint, suite completa (incluidas leyes), typecheck, compilación, pruebas de metadatos y precisión, esquema del evento, rechazo de un campo renombrado y segunda generación idéntica. [Evidencia y límites](../../ingenieria/diseno/HU-gateway-08.md). El servidor Connect y las operaciones del motor corresponden a 09 y 10.
- **Historia:**
  Como desarrollador integrador,
  quiero un contrato versionado de las operaciones y eventos de `kernel.v1`,
  para generar clientes y comprobar compatibilidad automáticamente.
- **Criterios de aceptación:**
  1. Dado `contracts/`, cuando se lista, entonces contiene los `.proto` de las operaciones definidas en el ADR 0037 y un JSON Schema por evento emitido por ellas.
  2. Dado un `.proto` con un campo renombrado respecto de `main`, cuando corre `buf breaking`, entonces falla.
  3. Dado el repositorio limpio, cuando se ejecuta `task gen`, entonces genera el código Go y TypeScript con plugins locales sin acceso a red, y un segundo `task gen` no cambia ningún archivo.
  4. Dado el contrato de la operación `money`, cuando se lee, entonces declara `idempotent = true`, riesgo `money`, permiso y capability.
  5. Dado el contrato, cuando se lee la lista de errores, entonces incluye `idempotency_conflict` y un código reintentable para `40001` y `40P01` (seguimiento 33), alineado con `docs/02-arquitectura/contrato.md`.
  6. Dado el CI, cuando corre, entonces ejecuta `buf lint`, `buf breaking` y `task gen` con verificación de que no hay diferencias.
- **Reglas de negocio:** contrato único (ADR-0004); riesgo, permiso e idempotencia declarados (`AGENTS.md` regla 6); `idempotency_conflict` ya existe en el kernel (M1, backlog `nucleo-del-kernel.md` P2); el código reintentable es nuevo (seguimiento 33).
- **Dependencias:** HU-gateway-01.
- **Fuera de alcance:** cliente SDK de uso (solo código generado), servidor, otros lenguajes.

## HU-gateway-09 · Registrar las primeras operaciones y componer el `Engine` (R2)

- **Estado:** Terminada e integrada en `sprint/S02` el 2026-11-16. **Tamaño:** S. **Rama:** `feature/HU-gateway-09`. **ADR exigidos:** 0037 (contrato, aceptado), 0038 y 0039 (actor y primer ingreso, aceptados).
- **Avance:** métodos fijos del motor, políticas generadas y código `retryable` implementados. [Diseño y pruebas](../../ingenieria/diseno/HU-gateway-09.md). La selección por slug y el directorio permanecen en HU-gateway-12; la puerta Connect permanece en HU-gateway-10.
- **Historia:**
  Como desarrollador integrador,
  quiero que la operación de intercambio con compromiso y la lectura de eventos existan en el kernel,
  para tener qué ejecutar por las puertas.
- **Criterios de aceptación:**
  1. Dado un espacio con un participante con el permiso de la operación `money`, cuando se ejecuta con `RunIdempotent` con una clave nueva, entonces se registran el intercambio con asientos que suman cero por recurso, el compromiso y el evento en una sola transacción.
  2. Dada la misma clave y el mismo cuerpo, cuando se repite, entonces la respuesta es la almacenada y no se crean filas nuevas.
  3. Dada la misma clave con otro cuerpo, cuando se repite, entonces falla con `idempotency_conflict`.
  4. Dada la operación `money`, cuando se llama por `Run` (sin idempotencia), entonces falla con `invalid_input`.
  5. Dado el participante sin el permiso, cuando ejecuta cualquiera de las dos, entonces recibe `permission_denied` y no se escribe nada.
  6. Dado `services/api/cmd/api/main.go`, cuando arranca, entonces crea un solo `Engine` por proceso y registra las operaciones desde `services/api`, sin crear `kernel/extension`.
  7. Dado el espacio B, cuando se lee con la operación de eventos en el espacio A, entonces no devuelve eventos de B (aislamiento).
- **Reglas de negocio:** `Run` rechaza operaciones idempotentes y `kernel*` externas (`kernel/execution/engine.go`); un `Engine` por proceso (ADR-0035); conservación y memoria (leyes, `kernel/laws`); idempotencia obligatoria en dinero (`AGENTS.md` regla 6, ADR-0026).
- **Dependencias:** HU-gateway-08, HU-gateway-02.
- **Fuera de alcance:** catálogo completo de operaciones, gateway.

## HU-gateway-10 · Servir las operaciones por Connect-RPC y JSON/HTTP (R3)

- **Estado:** Terminada e integrada en `sprint/S02` el 2026-11-16. **Tamaño:** L. **Rama:** `feature/HU-gateway-10`. **ADR exigidos:** 0037, 0038, 0039, 0040, aceptados (la autenticación entra con HU-gateway-11 y 12; esta historia usa un autenticador inyectable de pruebas).
- **Avance:** [servidor Connect, errores y límites](../../ingenieria/diseno/HU-gateway-10.md) implementados con pruebas de HTTP e idempotencia. El cableado del directorio por slug sigue en HU-gateway-12.
- **Historia:**
  Como desarrollador integrador,
  quiero llamar las operaciones por Connect-RPC y JSON/HTTP en `/{slug}/...`,
  para operar un espacio desde cualquier cliente.
- **Criterios de aceptación:**
  1. Dado `kernel/gateway`, cuando se revisa su constructor, entonces recibe solo el `Engine`; ninguna firma pública recibe un pool de DB y el paquete no importa `kernel/state` (prueba de arquitectura que falla si se rompe, también para `services/api`).
  2. Dada una llamada con un `Actor` provisto por el autenticador, cuando se ejecuta una operación válida, entonces responde 200 y `Call.Request` contiene los bytes del cuerpo sin reescribir.
  3. Dado el encabezado `Idempotency-Key`, cuando se llama la operación `money`, entonces llega a `Call.IdempotencyKey`; sin la clave responde con error tipado `invalid_input` y no ejecuta nada.
  4. Dado cada código tipado de `kernel/model` (`invalid_input`, `permission_denied`, `not_found`, `idempotency_conflict`, el reintentable), cuando la operación falla, entonces el cliente recibe el código Connect correspondiente, documentado en una tabla en `docs/04-integraciones/puertas.md`.
  5. Dada una operación que provoca un `PanicError`, cuando falla, entonces el cliente recibe un error genérico `internal` sin mensaje ni pila y el detalle queda solo en el log.
  6. Dada una operación que supera el tiempo máximo, cuando se cancela, entonces el `ctx` de la llamada ya lleva ese plazo y la respuesta es un error tipado (seguimientos 69 y 72).
  7. Dado un cliente que supera el límite por minuto, cuando llama de nuevo, entonces recibe `resource_exhausted` con espera indicada; el contador es en memoria y probado con reloj inyectable (ADR-0025).
  8. Dadas las operaciones `kernel.outbox.claim` y los marcadores internos, cuando se pide su ruta, entonces responde 404 (seguimiento 42).
  9. Dado un `Actor` con `ActorSystem` en el cuerpo, cabecera o parámetro de la petición, cuando se llama, entonces se ignora o se rechaza: el `Actor` sale solo del autenticador (ADR-0035).
  10. Dada una llamada sin autenticador válido, cuando se llama, entonces responde `unauthenticated` sin ejecutar nada.
- **Reglas de negocio:** ADR-0023 (rutas y dominios), ADR-0025 (límites en memoria, sin Redis), ADR-0035 (solo Engine, Actor desde token, `PanicError` sin código); seguimientos 42, 69, 72; regla de puerta de la cabecera. Los valores de plazo y límite son nuevos y se fijan en el PR con constantes configurables.
- **Dependencias:** HU-gateway-08, HU-gateway-09.
- **Fuera de alcance:** validación real de tokens (HU-gateway-11), llaves (HU-gateway-13), MCP, webhooks.

## HU-gateway-11 · Validar tokens JWT de Clerk (R4)

- **Estado:** Terminada e integrada en `sprint/S02` el 2026-11-16. **Tamaño:** M. **Rama:** `feature/HU-gateway-11`. **ADR exigidos:** 0040 (aceptado).
- **Avance:** [verificación de tokens y JWKS acotado](../../ingenieria/diseno/HU-gateway-11.md), con pruebas locales de rotación, caída, antigüedad, concurrencia y validación de claims. Sin cuenta real de Clerk ni cambios en la resolución por slug.
- **Historia:**
  Como desarrollador integrador,
  quiero que la API acepte solo tokens de Clerk válidos,
  para que nadie opere con un token falso, vencido o ajeno.
- **Criterios de aceptación:** (todas con un JWKS simulado local)
  1. Dado un token válido firmado con la clave del JWKS, `iss`, `azp` permitido y vigente, cuando se valida, entonces devuelve el `sub` y el resto de claims necesarios.
  2. Dado un token vencido (`exp` pasado), cuando se valida, entonces falla con `unauthenticated`.
  3. Dado un token con `nbf` futuro, cuando se valida, entonces falla.
  4. Dado un token firmado con una clave ajena, cuando se valida, entonces falla.
  5. Dado un token con `alg=none` o un algoritmo simétrico, cuando se valida, entonces falla.
  6. Dado un token con `iss` o `azp` fuera de la lista permitida, cuando se valida, entonces falla.
  7. Dado un `kid` desconocido, cuando se valida, entonces se renueva el JWKS una vez y, si sigue desconocido, falla; dentro del límite de frecuencia del ADR no se renueva otra vez.
  8. Dada una rotación (`kid` nuevo ya publicado), cuando se valida un token con el `kid` nuevo, entonces es aceptado tras la renovación.
  9. Dada la caída del emisor con caché vigente dentro del plazo del ADR, cuando se valida un token con `kid` conocido, entonces se acepta; pasado el plazo, se rechaza.
  10. Dado cualquier fallo, cuando se responde al cliente, entonces el mensaje es genérico y no revela el motivo; el motivo exacto queda en el log sin el token.
- **Reglas de negocio:** ADR 0040 (nuevo); `AGENTS.md` fase B3 (validación de Clerk); el token nunca se registra en logs.
- **Dependencias:** HU-gateway-04.
- **Fuera de alcance:** resolución del participante (HU-gateway-12), prueba con Clerk real (HU-gateway-22).

## HU-gateway-12 · Construir el `Actor` desde el token y elegir el espacio por la ruta (R5)

- **Estado:** Terminada e integrada en `sprint/S02` el 2026-11-16. **Tamaño:** M. **Rama:** `feature/HU-gateway-12`. **ADR exigidos:** 0038, 0039 y 0040, aceptados.
- **Avance:** [directorio, actor y cableado por slug](../../ingenieria/diseno/HU-gateway-12.md). La migración 00015 queda reservada para el directorio; no se reutiliza en llaves ni webhooks.
- **Historia:**
  Como desarrollador integrador,
  quiero que el servidor resuelva quién soy y en qué espacio opero a partir de mi token,
  para que nadie pueda hacerse pasar por otro participante.
- **Criterios de aceptación:**
  1. Dado un token válido de una identidad con participante activo en el espacio del `slug` de la ruta, cuando se llama, entonces el `Actor` tiene `IdentityID` y participante resueltos en servidor y la operación corre con ese actor.
  2. Dado un cuerpo o encabezado que afirma otro `IdentityID` o participante, cuando se llama, entonces se ignora; el actor sale solo del token (ADR-0035).
  3. Dado un token válido sin participante en ese espacio, cuando se llama, entonces recibe `permission_denied` genérico, igual en forma y tiempo de respuesta aproximado a un participante sin permiso, sin revelar si el espacio o la identidad existen (ADR 0038).
  4. Dado un `slug` inexistente, cuando se llama, entonces recibe el mismo `permission_denied` genérico.
  5. Dado un participante suspendido o dado de baja, cuando se llama, entonces se rechaza con `permission_denied`.
  6. Dado el código de `execution` o del paquete autorizado por el ADR, cuando se revisa, entonces la regla de importación de `state` solo se excepciona donde el ADR 0037 lo permite; la prueba de arquitectura sigue en verde y falla ante otro uso.
  7. Dada la puerta, cuando se revisa, entonces la resolución no está en `kernel/gateway` y la puerta sigue recibiendo solo el `Engine`; `ActorSystem` no se puede construir desde ninguna ruta de petición (prueba que lo comprueba).
- **Reglas de negocio:** ADR-0035 (límites 1 y 3, seguimientos 4, 28, 29, 46), ADR-0023 (slug), ADR-0006 (identidad); sin enumeración (Product Owner, P4); regla de puerta.
- **Dependencias:** HU-gateway-02, HU-gateway-10, HU-gateway-11.
- **Fuera de alcance:** creación de identidades o participantes, invitaciones.

## HU-gateway-13 · Autenticar con llaves por espacio y alcances (R6)

- **Estado:** Lista. **Tamaño:** L. **Rama:** `feature/HU-gateway-13`. **ADR exigidos:** 0045.
- **Historia:**
  Como desarrollador integrador (n8n o agente `api_key`),
  quiero llaves por espacio con alcances limitados,
  para automatizar sin compartir un token de persona.
- **Criterios de aceptación:**
  1. Dada la migración `api_keys` (número 00015 o superior), cuando se ejecuta `task db:migrate` sobre una base vacía y sobre una ya migrada, entonces crea la tabla con RLS por espacio y no cambia nada la segunda vez.
  2. Dado un participante con el permiso de crear llaves, cuando crea una con `environment = live` y alcances dentro de sus permisos, entonces la respuesta muestra el secreto completo una sola vez, con prefijo identificable, y en base de datos solo hay hash.
  3. Dado un alcance fuera de los permisos del participante, cuando se crea la llave, entonces falla con `permission_denied`.
  4. Dada una llave válida, cuando se llama una operación dentro de sus alcances, entonces se ejecuta con el `Actor` del participante de la llave y `IdentityID` resuelto en servidor.
  5. Dada una llave llamando una operación fuera de sus alcances, cuando se llama, entonces recibe `permission_denied`.
  6. Dada una llave `environment = test`, cuando se usa en el entorno `live`, entonces se rechaza.
  7. Dada una llave revocada, cuando se usa, entonces falla con `unauthenticated`, de inmediato.
  8. Dada la rotación de una llave, cuando se completa, entonces la nueva funciona, la anterior queda revocada y el secreto nuevo se muestra una sola vez.
  9. Dada la lectura posterior de una llave, cuando se consulta, entonces nunca devuelve el secreto, solo el prefijo y metadatos.
  10. Dada la puerta, cuando se revisa, entonces las llaves se verifican sin entregar el pool a la puerta (solo el `Engine`) y el secreto nunca se escribe en logs.
- **Reglas de negocio:** ADR 0045 (nuevo); `api_keys` no existe hoy; regla 3 (solo el kernel toca la base); ADR-0035; regla de puerta; mutaciones que no mueven dinero, sin idempotencia obligatoria pero la el ADR-0045 propone no guardar respuesta idempotente para creación o rotación, porque contienen el secreto de presentación única.
- **Dependencias:** HU-gateway-05, HU-gateway-10, HU-gateway-12.
- **Fuera de alcance:** `ip_allowlist`, interfaz de gestión (consola, B5).

## HU-gateway-14 · Servir el MCP de solo lectura en `/mcp` (R7) (*)

- **Estado:** Lista, con supuesto por confirmar (S-P1). **Tamaño:** M. **Rama:** `feature/HU-gateway-14`. **ADR exigidos:** 0044, 0036, 0045.
- **Historia:**
  Como agente de IA con identidad propia,
  quiero consultar el kernel por herramientas MCP de lectura,
  para analizar un espacio sin poder modificarlo.
- **Criterios de aceptación:**
  1. Dado el contrato, cuando se genera el MCP, entonces hay una herramienta por cada operación `read` y ninguna por operaciones `write` o `money`.
  2. Dado un agente con llave de su participante propio, cuando llama la herramienta de lectura de eventos, entonces recibe los eventos de su espacio.
  3. Dada una llamada MCP a una operación `write` o `money` por nombre, cuando se envía, entonces se rechaza por no existir la herramienta.
  4. Dada una llamada sin credencial o con una revocada, cuando se envía, entonces recibe `unauthenticated`.
  5. Dado un agente cuya llave no incluye el alcance de lectura, cuando llama, entonces recibe `permission_denied`.
  6. Dada la puerta, cuando se revisa, entonces recibe solo el `Engine`, no importa `kernel/state` y el `Actor` sale de la llave validada (regla de puerta; ADR-0035).
  7. Dada la prueba de paridad de ADR-0020, cuando falta una operación `read` en el MCP, entonces la prueba falla; cuando falta una `write`, no falla.
- **Reglas de negocio:** `AGENTS.md` y `puertas.md` (solo lectura al principio); ADR-0023 (`/mcp` en la misma API); ADR-0020 (paridad, enmendada por 0044); el agente es un participante con identidad propia (brief); S-P1.
- **Dependencias:** HU-gateway-03, HU-gateway-10, HU-gateway-13.
- **Fuera de alcance:** MCP de escritura, recursos, prompts, `services/ai`.

## HU-gateway-15 · Firmar y entregar webhooks desde el outbox (R8a)

- **Estado:** Terminada e integrada en `sprint/S02` el 2026-11-16. **Tamaño:** L. **Rama:** `feature/HU-gateway-15`. **ADR exigidos:** 0041, 0042 y 0043 (programador, aceptado en HU-gateway-07).
- **Avance:** [Diseño, pruebas y límites](../../ingenieria/diseno/HU-gateway-15.md).
- **Historia:**
  Como sistema receptor de webhooks,
  quiero recibir los eventos del kernel firmados una sola vez por entrega,
  para reaccionar sin consultar y verificar que vienen de NexusThrive.
- **Criterios de aceptación:**
  1. Dadas las migraciones `webhook_endpoints` y `webhook_deliveries` (RLS por espacio, secreto cifrado de forma reversible), cuando se ejecuta `task db:migrate` sobre base vacía y ya migrada, entonces se aplican sin error y repetirla no cambia nada.
  2. Dado un participante con permiso, cuando registra un endpoint HTTPS, entonces se muestra el secreto una sola vez y en base de datos no hay texto plano.
  3. Dado un endpoint `http://` o que resuelve a una dirección privada o de loopback fuera de `local`, cuando se registra, entonces falla con `invalid_input`.
  4. Dado un evento en el outbox del espacio, cuando el despachador lo reclama (`kernel.outbox.claim`) y lo entrega, entonces el receptor recibe un POST con `event.id` estable, cabecera de firma HMAC con marca de tiempo calculada sobre el cuerpo exacto y el receptor la verifica con el secreto.
  5. Dado el reparto, cuando confirma su transacción, el outbox queda `sent` por aceptación duradera del trabajo. Dada una entrega con respuesta 2xx, se registra su resultado en `webhook_deliveries` y una fila histórica de intento con estado, código y duración (ADR-0041).
  6. Dado el mismo evento entregado dos veces (entrega al menos una vez), cuando llega el segundo POST, entonces lleva el mismo `event.id` para que el receptor deduplique.
  7. Dados varios espacios con eventos pendientes, cuando el despachador trabaja, entonces rota entre los espacios activos y ninguno monopoliza el ciclo.
  8. Dado un evento de un espacio A, cuando se despacha, entonces nunca se entrega a un endpoint del espacio B (aislamiento).
  9. Dado el despachador, cuando se revisa, entonces llama al kernel solo por el `Engine` con un actor del sistema construido por el propio proceso, sin pool ni importar `kernel/state` (regla de puerta).
  10. Dado el cuerpo enviado, cuando se revisa, entonces nunca contiene el secreto de la llave ni datos de otro espacio.
- **Reglas de negocio:** ADR-0031 (reclamo y arrendamiento, al menos una vez), ADR-0028 (orden de eventos), seguimientos 13, 39; ADR 0041, 0042 y 0043 (aceptados); `webhook_*` no existe hoy; firma HMAC (`puertas.md`).
- **Dependencias:** HU-gateway-06, HU-gateway-07, HU-gateway-10, HU-gateway-09.
- **Fuera de alcance:** reintentos con espera y deshabilitación (HU-gateway-16), reenvío y rotación (HU-gateway-17), `/webhooks/wompi`.

## HU-gateway-16 · Reintentar entregas y deshabilitar endpoints con fallos (R8b)

- **Estado:** Terminada e integrada en `sprint/S02` el 2026-11-16. **Tamaño:** M. **Rama:** `feature/HU-gateway-16`. **ADR exigidos:** 0041.
- **Avance:** [Política, pruebas y límites](../../ingenieria/diseno/HU-gateway-16.md).
- **Historia:**
  Como sistema receptor de webhooks,
  quiero que las entregas fallidas se reintenten con espera creciente y que un endpoint roto se deshabilite,
  para recuperarme de caídas cortas sin que el kernel insista sin fin.
- **Criterios de aceptación:**
  1. Dada una entrega que falla (timeout o no-2xx), cuando se reintenta, entonces las esperas siguen la tabla creciente del ADR 0041, probada con reloj inyectable.
  2. Dado un evento cuyo outbox llega al quinto fallo, cuando se registra, entonces pasa a `dead` y no se reclama más (ADR-0031).
  3. Dado un endpoint con 20 fallos seguidos, cuando ocurre el vigésimo, entonces queda deshabilitado, no recibe más entregas y se registra el motivo.
  4. Dado un endpoint con 19 fallos seguidos y luego un éxito, cuando se cuenta, entonces el contador de fallos seguidos vuelve a 0.
  5. Dado un endpoint deshabilitado, cuando se consultan sus entregas, entonces siguen visibles con su historial.
  6. Dados `docs/02-arquitectura/plataforma/flujos.md` y `docs/04-integraciones/puertas.md`, cuando se revisan, entonces las cifras coinciden con el ADR 0041.
  7. Dada una entrega que reintenta, cuando se envía de nuevo, entonces conserva el mismo `event.id` y recalcula la firma con una marca de tiempo nueva.
- **Reglas de negocio:** ADR-0031 (`dead` al quinto fallo), `puertas.md` (20 fallos), ADR 0041 (tabla de esperas, nueva).
- **Dependencias:** HU-gateway-15.
- **Fuera de alcance:** reenvío manual, rotación.

## HU-gateway-17 · Reenviar entregas y rotar el secreto del endpoint (R8c)

- **Estado:** Implementación local validada; pendiente de CI e integración. **Tamaño:** M. **Rama:** `feature/HU-gateway-17`. **ADR exigidos:** 0042.
- **Avance:** [Diseño, pruebas y límites](../../ingenieria/diseno/HU-gateway-17.md).
- **Historia:**
  Como desarrollador integrador,
  quiero reenviar una entrega y rotar el secreto sin perder eventos,
  para recuperar datos y cambiar credenciales con seguridad.
- **Criterios de aceptación:**
  1. Dada una entrega en `webhook_deliveries` fallida o muerta, cuando un participante con permiso pide el reenvío, entonces se envía de nuevo con el mismo `event.id`, se registra un intento nuevo y el original se conserva.
  2. Dado un participante sin permiso, cuando pide el reenvío, entonces recibe `permission_denied`.
  3. Dado un endpoint deshabilitado, cuando se pide un reenvío, entonces falla con un error tipado sin enviar.
  4. Dada la rotación del secreto, cuando se completa, entonces se muestra el secreto nuevo una sola vez y durante la ventana del ADR 0042 las entregas llevan firmas válidas para ambos secretos.
  5. Dada la ventana vencida, cuando se entrega, entonces solo vale el secreto nuevo.
  6. Dado un reenvío de un evento de otro espacio, cuando se pide, entonces responde `not_found` (aislamiento).
  7. Dada la puerta, cuando se revisa, entonces opera solo por el `Engine`, el actor sale del token o llave y el secreto no aparece en logs (regla de puerta).
- **Reglas de negocio:** ADR 0042 (nuevo): secreto reversible con ventana de doble secreto; `puertas.md` (reenvío manual y rotación).
- **Dependencias:** HU-gateway-15.
- **Fuera de alcance:** interfaz (consola), re-despacho masivo.

## HU-gateway-18 · Demostrar el criterio de terminado de B3 de extremo a extremo

- **Estado:** Lista. **Tamaño:** S. **Rama:** `feature/HU-gateway-18`. **ADR exigidos:** ninguno nuevo (0036, 0037, 0040, 0042).
- **Historia:**
  Como equipo NexusThrive,
  quiero una prueba automática del criterio de B3,
  para saber que las puertas funcionan juntas.
- **Criterios de aceptación:**
  1. Dado un PostgreSQL de `task infra:up`, un JWKS simulado, un espacio con participante y un endpoint receptor local, cuando la prueba llama la operación `money` por la API con un token de Clerk válido y una `Idempotency-Key`, entonces la operación se ejecuta y el intercambio queda con asientos en cero.
  2. Dada la misma llamada repetida con la misma clave, cuando se repite, entonces la respuesta es idéntica y no hay filas duplicadas en el libro, el compromiso ni el outbox.
  3. Dada la ejecución, cuando el despachador corre, entonces el receptor recibe exactamente un evento firmado con firma verificable y `event.id` estable.
  4. Dada la misma llamada con un token vencido, cuando se envía, entonces falla con `unauthenticated` y no se escribe nada.
  5. Dada la prueba, cuando corre en CI con `task test`, entonces no requiere red externa ni cuentas.
- **Reglas de negocio:** hoja de ruta, criterio de terminado de B3 (literal en la definición); conservación, memoria y comprometer (leyes, `kernel/laws`).
- **Dependencias:** HU-gateway-10, 11, 12, 15.
- **Fuera de alcance:** Clerk real (HU-gateway-22), staging.

## HU-gateway-19 · Generar el nodo n8n con acciones y trigger por eventos (R9) (*)

- **Estado:** Lista. **Tamaño:** M. **Rama:** `feature/HU-gateway-19`. **ADR exigidos:** 0036, 0039 (alcance coherente con el MCP), 0041.
- **Historia:**
  Como integrador que usa n8n,
  quiero un nodo generado desde el contrato con acciones y trigger por eventos,
  para automatizar avisos sin que n8n guarde ni mueva valor.
- **Criterios de aceptación:**
  1. Dado el contrato, cuando se genera `integrations/n8n/`, entonces hay una acción por operación del contrato y un trigger que recibe los webhooks firmados.
  2. Dado el trigger, cuando recibe un POST con firma inválida o marca de tiempo fuera de ventana, entonces lo rechaza sin disparar el flujo.
  3. Dado el nodo configurado con una llave por espacio, cuando ejecuta la acción de lectura de eventos contra la API local, entonces devuelve los eventos del espacio.
  4. Dada la acción `money`, cuando se ejecuta, entonces envía una `Idempotency-Key` estable por ejecución del flujo y el nodo no calcula ni guarda saldos.
  5. Dadas las pruebas unitarias del nodo, cuando corren con `task test`, entonces pasan sin instancia n8n real.
  6. Dada la prueba de paridad de ADR-0020, cuando falta una operación en el nodo, entonces falla.
  7. Dado el repositorio, cuando se revisa, entonces no contiene plantillas de flujos que dependan de Wompi o de Business.
- **Reglas de negocio:** ADR-0005 (n8n nunca guarda ni mueve valor), ADR-0020 (nodo generado y paridad), `AGENTS.md` regla 8; alcance solo nodo y trigger (P11 por defecto); la licencia comercial de n8n bloquea N3, no B3.
- **Dependencias:** HU-gateway-08, HU-gateway-13, HU-gateway-15.
- **Fuera de alcance:** plantillas de flujos, instancia n8n real, n8n de staging (HU-gateway-23).

## HU-gateway-20 · Empaquetar la API, migrar al arrancar y exponer salud (R10)

- **Estado:** Lista. **Tamaño:** M. **Rama:** `feature/HU-gateway-20`. **ADR exigidos:** ninguno nuevo (ADR-0019, ADR-0024 ya aceptados).
- **Historia:**
  Como equipo NexusThrive,
  quiero una imagen de la API que migre antes de recibir tráfico y use el rol limitado,
  para desplegar de forma segura.
- **Criterios de aceptación:**
  1. Dado `services/api/Dockerfile`, cuando se construye, entonces la imagen arranca el binario sin shell de desarrollo ni secretos incluidos.
  2. Dada una base vacía, cuando arranca la API, entonces aplica las migraciones antes de abrir el puerto y, si una migración falla, termina con código distinto de 0 sin aceptar tráfico.
  3. Dado el endpoint de salud, cuando la base responde, entonces devuelve 200; cuando no responde, devuelve 503, y no expone versiones ni errores internos.
  4. Dado el rol limitado de la API, cuando la API inicia sesión contra el PostgreSQL local, entonces usa contraseña como verificador SCRAM (seguimientos 1 y 2) y una prueba de inicio de sesión real con ese rol pasa.
  5. Dado el log de arranque, cuando se revisa, entonces no contiene la contraseña ni el verificador.
  6. Dado el rol limitado, cuando intenta escribir en un esquema ajeno o saltarse RLS, entonces falla.
- **Reglas de negocio:** migraciones antes de recibir tráfico (ADR-0019); roles y RLS (ADR-0027); herramienta de migraciones (ADR-0024); seguimientos 1 y 2.
- **Dependencias:** HU-gateway-10.
- **Fuera de alcance:** despliegue en línea (HU-gateway-23), observabilidad (B6).

## HU-gateway-21 · Arrancar el programador de timers y el despachador en la API (R-P13) (*)

- **Estado:** Lista; S-P13 y ADR aceptados. **Tamaño:** S. **Rama:** `feature/HU-gateway-21`. **ADR exigidos:** 0043.
- **Historia:**
  Como equipo NexusThrive,
  quiero que la API dispare los timers vencidos y despache el outbox por sí misma,
  para que los compromisos venzan y los eventos salgan sin otro proceso.
- **Criterios de aceptación:**
  1. Dado un compromiso con timer vencido, cuando corre el ciclo con reloj inyectable, entonces se ejecuta `kernel.timer.fire` por el `Engine`, el compromiso pasa a `expired` y se escribe `kernel.commitment.expired`.
  2. Dado un evento en el outbox, cuando corre el ciclo, entonces el despachador lo procesa.
  3. Dado un fallo en el ciclo, cuando ocurre, entonces el ciclo se registra y continúa en la siguiente vuelta sin tumbar la API.
  4. Dada la API detenida con una señal, cuando termina, entonces el ciclo se cancela por `ctx` y no deja trabajo reclamado sin arrendar.
  5. Dado el ciclo, cuando se revisa, entonces usa solo el `Engine` con un actor `ActorSystem` que construye el propio proceso (regla de puerta) y declara en logs el límite de una sola instancia.
  6. Dado `kernel/coordination` y `scheduler_runs`, cuando se revisa el repositorio, entonces no se crean.
- **Reglas de negocio:** ADR-0034 (vencimiento por timer), ADR-0031, ADR 0043 (nuevo); S-P13.
- **Dependencias:** HU-gateway-07, HU-gateway-15.
- **Fuera de alcance:** multi-instancia, coordinación.

---

# Parte B: cuentas externas

## HU-gateway-22 · Probar con un token real de Clerk de desarrollo (R11)

- **Estado:** **Bloqueada.** **Tamaño:** S. **Rama:** `feature/HU-gateway-22`. **ADR exigidos:** 0040.
- **Pregunta o cuenta que la desbloquea:** Q1, instancia de desarrollo de Clerk con sus `iss`, `azp` y acceso a la clave pública.
- **Historia:**
  Como equipo NexusThrive,
  quiero una prueba de humo con un token emitido por Clerk,
  para confirmar que la validación funciona con el emisor real.
- **Criterios de aceptación:**
  1. Dada la instancia de desarrollo de Clerk y un usuario de prueba con participante sembrado, cuando se obtiene un token y se llama la lectura de eventos, entonces responde 200.
  2. Dado el mismo token tras cerrar la sesión o vencer, cuando se llama, entonces responde `unauthenticated`.
  3. Dado el token de otra aplicación de Clerk, cuando se llama, entonces se rechaza por `azp` o `iss`.
  4. Dada la prueba, cuando corre en CI, entonces se omite sin falla si faltan las credenciales y se ejecuta como tarea manual `task smoke:clerk`; las credenciales no entran al repositorio.
- **Reglas de negocio:** ADR 0040; secretos por entorno (ADR-0019).
- **Dependencias:** HU-gateway-11, HU-gateway-12, Q1.
- **Fuera de alcance:** producción, creación de usuarios en masa.

## HU-gateway-23 · Publicar el staging en línea de la API (R12) (*)

- **Estado:** **Bloqueada.** **Tamaño:** L. **Rama:** `feature/HU-gateway-23`. **ADR exigidos:** ninguno nuevo (ADR-0019, ADR-0023, ADR-0025).
- **Pregunta o cuenta que la desbloquea:** Q2, proyecto Supabase de staging, servidor con Dokploy y control del DNS `nexusthrive.com.co`. El alcance sin Vercel depende de S-P12.
- **Historia:**
  Como equipo NexusThrive,
  quiero la API desplegada en `staging-api.nexusthrive.com.co`,
  para validar con el equipo y con integradores.
- **Criterios de aceptación:**
  1. Dado un merge a `main`, cuando corre el flujo de GitHub Actions, entonces Dokploy despliega la imagen de HU-gateway-20 y las migraciones se aplican antes de recibir tráfico.
  2. Dado `https://staging-api.nexusthrive.com.co/health`, cuando se consulta, entonces responde 200 con certificado válido.
  3. Dado el proyecto Supabase de staging, cuando se revisa, entonces usa el pooler en sesión, con Data API y Auth desactivados, y la API entra con el rol limitado.
  4. Dados los secretos, cuando se revisa el repositorio, entonces están solo en secretos de GitHub y de Dokploy por entorno, nunca en el código.
  5. Dado el n8n de staging, cuando se revisa, entonces usa credenciales separadas de otros entornos.
  6. Dado el alcance (S-P12), cuando se revisa, entonces no hay Vercel, web, consola, Redis ni dominios `staging.` y `staging-console.`.
  7. Dado el flujo con una migración fallida, cuando corre, entonces el despliegue se detiene y la versión anterior sigue sirviendo.
- **Reglas de negocio:** ADR-0019 (Dokploy desde `main`, Supabase), ADR-0023 (dominios), ADR-0025 (sin Redis), `docs/07-ingenieria/ci-cd.md`.
- **Dependencias:** HU-gateway-20, HU-gateway-21 (si S-P13 se confirma), Q2.
- **Fuera de alcance:** producción, web, consola, observabilidad.
