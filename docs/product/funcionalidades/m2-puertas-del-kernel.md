# Funcionalidad: M2, las puertas del kernel (B3)

Fuente de contexto: `docs/product/contexto-dominio.md`. Hoja de ruta: `docs/02-arquitectura/hoja-de-ruta.md` (M2 = B3). Esta definición agrupa varias rebanadas verticales (R0 a R9) que el Analista de Requisitos convertirá en historias; cada rebanada se puede entregar y probar por separado.

## Problema a resolver

El kernel terminó M1 con motor, leyes y pruebas, pero nadie puede llamarlo. `kernel/gateway` no existe, `contracts/` no tiene un solo `.proto`, `services/api/cmd/api/main.go` tiene un `main()` vacío y ninguna operación fuera de `kernel.timer.fire` y `kernel.outbox.claim` está declarada (brief, "Módulos relacionados" y "Paquetes que B3 consumirá"). Sin puertas, ningún cliente, agente, integración ni equipo de plataforma puede usar el kernel, y las cinco leyes no se demuestran de extremo a extremo. Además, ADR-0035 dejó dos límites aceptados que B3 debe cerrar: el actor lo afirma la aplicación y existe una ruta alternativa por el pool.

Frase del problema: el kernel es correcto pero inalcanzable; M2 construye las puertas seguras (API, MCP de lectura, webhooks firmados, Clerk, n8n) y el despliegue a staging.

## Usuario objetivo

- **Desarrollador o integrador** que llama la API con token de Clerk o llave por espacio (persona de negocio, agente `api_key`, n8n).
- **Agente de IA** (participante con identidad propia) que consulta por MCP.
- **Sistema receptor** de webhooks firmados (n8n u otro).
- **Equipo NexusThrive** (staff): recibe un staging en línea para validar. No usa aún la consola (B5).
- Rol de plataforma: los demás roles del kernel (`owner`, etc.) se benefician indirectamente al poder operar.

## Resultado esperado (outcome)

Criterio de terminado de B3, literal de la hoja de ruta y probado automáticamente: una operación entra por la API con token de Clerk, se ejecuta, se repite con la misma clave de idempotencia sin duplicarse y su señal llega a un webhook firmado.

Observables adicionales:

1. Ninguna puerta recibe el pool de DB ni importa `kernel/state`; solo el `Engine` (prueba de arquitectura).
2. El `Actor` se construye únicamente desde un token validado (o llave); `ActorSystem` jamás desde una petición.
3. Un `PanicError` llega al cliente como error genérico sin mensaje ni pila.
4. La misma operación declarada en el contrato existe en API, SDK, herramienta MCP (si es de lectura), evento y nodo n8n, y una prueba falla si falta en una puerta (ADR-0020).
5. Paridad de staging: la API responde en `staging-api.nexusthrive.com.co` con migraciones aplicadas antes de recibir tráfico (solo este punto depende de cuentas externas).

## Alcance

### Incluido

Parte A, no requiere cuentas externas (se puede cerrar localmente con PostgreSQL de `task infra:up` y JWKS simulado):

- **R0 Decisiones:** ADR previos a código (ver tabla de preguntas abiertas).
- **R1 Contratos:** primeros `.proto` de `kernel.v1`, JSON Schema de eventos, `buf lint`/`buf breaking`, `task gen`.
- **R2 Primera operación y composición:** declarar y registrar la(s) operación(es) mínima(s) que demuestran el criterio, con cableado en `services/api` (un `Engine` por proceso).
- **R3 Gateway Connect-RPC y JSON/HTTP:** `kernel/gateway` que solo recibe el `Engine`; traducción de errores tipados a códigos Connect; `PanicError` genérico; `Call.Request` con el cuerpo sin reescribir; clave de idempotencia a `Call.IdempotencyKey`; tiempo máximo en `ctx` (seguimientos 69, 72); límites de tasa en memoria (ADR-0025); no se exponen `ClaimOutbox` ni marcadores (seguimiento 42).
- **R4 Validación de tokens de Clerk:** JWT con firma, `iss`, `exp`, `nbf`, `azp`/`aud`, algoritmo permitido, caché de JWKS y comportamiento ante caída; pruebas con JWKS simulado (token vencido, firma ajena, `kid` desconocido, `alg=none`, rotación).
- **R5 Resolución token a participante y construcción del `Actor`:** `IdentityID` siempre resuelto en servidor (seguimiento 4); selección del espacio por la ruta con `slug` (ADR-0023).
- **R6 Llaves por espacio con alcances:** migración `api_keys` (00015 en adelante), hash, vista única del secreto, revocación, rotación, prefijo y `environment`. Necesaria para n8n y MCP.
- **R7 MCP de solo lectura** en `/mcp`, con identidad propia del agente y herramientas generadas de las operaciones `read`.
- **R8 Webhooks salientes firmados:** migraciones `webhook_endpoints` y `webhook_deliveries`, firma HMAC, reintentos con espera creciente, registro de entregas, reenvío manual, rotación de secreto, deshabilitación por fallos seguidos, despacho del outbox con deduplicación por `event.id` (seguimientos 13, 39), disparo de entrega desde un proceso en la API.
- **R9 Nodo n8n generado** (acciones y trigger por eventos) con pruebas unitarias y contra API local; sin instancia n8n real.
- **R10 Imagen y salud:** Dockerfile de `services/api`, migraciones al arrancar, endpoint de salud, contraseña del rol de la API como verificador SCRAM sin registrar en logs y prueba de inicio de sesión real con el rol limitado contra el PostgreSQL local (seguimientos 1 y 2).
- Traducción de lo anterior en docs: corregir las cifras de reintentos de `flujos.md`, y alinear MCP en `AGENTS.md`/`puertas.md`/ADR-0018/0020.

Parte B, requiere cuentas externas (bloqueada):

- **R11 Prueba de humo con token real de Clerk** (instancia de desarrollo).
- **R12 Staging en línea:** proyecto Supabase de staging (pooler en sesión, Data API y Auth desactivados), Dokploy con despliegue desde `main`, DNS `staging-api.nexusthrive.com.co` con certificado, secretos por entorno, flujo de despliegue en GitHub Actions, n8n de staging con credenciales separadas.

### Excluido explícitamente

- MCP de escritura, recursos y prompts avanzados, SDK en otros lenguajes (ADR-0018, pospuestos).
- Plantillas de flujos n8n que dependen de Wompi o Business (prueba por vencer, pago recibido, existencias bajas); licencia comercial de n8n (bloquea N3, no B3).
- `/webhooks/wompi` y todo cobro (B4, ADR-0022).
- Web, consola, Vercel y dominios `staging.` y `staging-console.`; Redis de staging.
- Producción (Render, Supabase de pago).
- Plataforma (staff, planes, cobros, códigos de beneficio), MFA y observabilidad (B4, B6).
- `kernel/coordination`, `scheduler_runs`, `kernel/extension` (salvo decisión del dueño en P5 y P13).
- Seguimientos 21, 43, 47, 10, 63, 68 (B4/B6), y los pospuestos 35, 56 salvo que P14 diga otra cosa.
- Funciones fuera de lo pedido: alta masiva de identidades, invitaciones, avisos (`notices`), archivos.

## Valor y prioridad

- **Valor para el usuario:** convierte el kernel en un producto utilizable: sin API ni webhooks nadie puede crear, consultar ni reaccionar a nada. Cada puerta cierra un límite de seguridad de ADR-0035 (actor certificado, sin pool), lo que habilita a B4 y B5.
- **Valor para el negocio:** B3 es el siguiente paso obligatorio de la etapa 1; B4 y B5 dependen de él, y el staging permite validar con el equipo y con integradores antes de Business (regla 11 de AGENTS.md: Business empieza tras B5).
- **Esfuerzo relativo:** XL en total. Por rebanada: R1 M, R2 S, R3 L, R4 M, R5 M, R6 L, R7 M, R8 XL, R9 M, R10 M, R11 S, R12 L.
- **Prioridad:**
  - Must: R0 a R5, R8 (mínimo para el criterio de B3), R10.
  - Must (cierre de M2): R6 y R7, R9, porque la hoja de ruta y ADR-0018 los incluyen en B3.
  - Must pero bloqueado externamente: R11 y R12 (solo "staging en línea" depende de cuentas).
  - Won't en M2: lo listado en Excluido.
  - Orden sugerido: R0, R1, R2, R3, R4, R5 (camino crítico del criterio), luego R8, R10, en paralelo R6, R7, R9; al final R11 y R12 cuando haya cuentas.

## Supuestos y riesgos de producto

- Supuesto: el criterio de B3 se demuestra localmente con JWKS simulado; staging no bloquea el cierre técnico de la Parte A.
- Riesgo: R8 es la rebanada más grande (migraciones, ADR de despacho, secreto, reintentos); puede desbordar el sprint. Mitigación: partirla en firma+entrega, reintentos+deshabilitación, reenvío+rotación.
- Riesgo: la ruta por el pool sigue existiendo en el proceso; la prueba de arquitectura debe ser estricta y cubrir `services/api` y el gateway.
- Riesgo: contradicción de MCP entre documentos; si no se resuelve, el MCP y el nodo n8n se generarían con alcance distinto.
- Riesgo: dependencia de `buf.build` (plugins remotos) en CI sin red o con límites de uso.
- Riesgo: sin reintento ante `40001`/`40P01` el cliente verá errores sin tipo (seguimiento 33); se mitiga con el código reintentable del contrato.
- Riesgo: sin proceso programador, los compromisos vencidos no expiran en staging (límite aceptado 3).
- Supuesto: una operación mínima de lectura y una de escritura bastan para demostrar el criterio; no se diseña el catálogo completo.

## Decisiones que requieren al usuario

Cada pregunta abierta del brief con decisión propuesta por defecto. "ADR" = el dueño debe aprobar un ADR antes del código (regla 12).

| # | Pregunta | Decisión por defecto propuesta | Requiere dueño / ADR |
|---|---|---|---|
| 1 | MCP: solo lectura o lectura y escritura | Solo lectura en M2 (lo pide la petición, AGENTS.md y puertas.md). Se corrige ADR-0018/0020 con una enmienda: la escritura con propuestas aprobadas por persona queda pospuesta | Sí: enmienda de ADR (contradicción entre documentos vigentes) |
| 2 | Primera operación del contrato | Una operación de escritura `money` de `kernel.v1` que registre un intercambio con compromiso (`kernel.exchange.write`, idempotente obligatoria) y una de lectura de eventos (`kernel.event.read`) para MCP y webhooks. Nombres y forma exactos los fija el ADR de contrato | Sí: ADR de la lista inicial de `kernel.v1` |
| 3 | Resolución token a participante y selección del espacio | Se resuelve dentro de `execution` (o paquete permitido por la prueba de arquitectura), nunca en la puerta; el espacio viene en la ruta por `slug` (ADR-0023) | Sí: ADR (toca la regla de importación de `state`) |
| 4 | Identidad en el primer ingreso | Sin creación automática en M2: la identidad y el participante se crean por operación previa (sembrado/invitación futura) y un token sin participante recibe `permission_denied` genérico. Sin enumeración | Sí: ADR (seguimiento 6) |
| 5 | Alcance de `kernel/extension` | No se crea en M2; la composición de operaciones vive en `services/api` y pasa solo el `Engine` | No, queda como decisión de diseño documentada en el ADR de la pregunta 3 |
| 6 | Claims de Clerk y caída del JWKS | Validar `iss`, `exp`, `nbf`, `azp` con lista permitida, solo algoritmos asimétricos; caché de JWKS con renovación ante `kid` desconocido (con límite de frecuencia); ante caída del emisor se usa la caché vigente hasta un plazo máximo y luego se rechaza | Sí: ADR |
| 7 | Llaves por espacio en M2 | Entran (n8n y MCP dependen de ellas). Secreto de un solo uso visible, solo hash en BD, prefijo identificable, `environment` `live`/`test`, alcances como subconjunto de permisos del participante. `ip_allowlist` queda fuera | Sí: ADR y migración |
| 8 | Despacho del outbox y cifras de reintentos | Tres cifras son tres cosas distintas: el outbox pasa a `dead` al quinto fallo (ADR-0031); el endpoint se deshabilita tras 20 fallos seguidos (puertas.md); "hasta 8" de `flujos.md` se corrige a lo que fije el ADR. Despachador dentro del binario de la API, rotación entre espacios activos | Sí: ADR (seguimientos 13, 39) |
| 9 | Secreto de webhooks (`secret_hash` vs HMAC) | Guardar el secreto cifrado de forma reversible (clave de aplicación por entorno), no un hash; mostrar el secreto una vez; rotación con ventana donde valen ambos. Corregir el modelo de datos | Sí: ADR (afecta seguridad y modelo) |
| 10 | `buf` y plugins remotos; Python | Preferir plugins locales fijados por versión para que CI no dependa de la red; Python fuera de M2 (el SDK de otros lenguajes está pospuesto) y se corrige `contrato.md` | No, decisión de ingeniería; informar al dueño |
| 11 | Alcance de n8n | Solo nodo y trigger generados; sin plantillas de flujos de Business/Wompi | No, coincide con ADR-0018/0020; confirmar |
| 12 | Alcance de "staging en línea" | Solo API, base de staging, n8n de staging y DNS de la API; sin Vercel, web, consola ni Redis | Confirmar (cambia alcance si el dueño quiere web) |
| 13 | Proceso programador | Arrancar `FireDueTimers` y el despachador del outbox en el binario de la API con un ciclo simple, sin `kernel/coordination` ni multi-instancia (una instancia en staging) | Sí: ADR (seguimientos 60 y 61) o se pospone a M4 con compromisos sin expirar en staging |
| 14 | Seguimientos 35, 56, 63, 68 | Siguen "Después de M1"; no entran en M2 | No |
| 15 | Lecturas de M1 sin confirmar | Fuera de B3; el dueño las confirma al revisar el reporte | Solo confirmación del dueño |
| 16 | PR #1 con menciones no admitidas | Fuera de B3; decisión de proceso del dueño (dejarlo cerrado o editar la descripción) | Decisión del dueño, sin ADR |

**ADR a redactar antes del código (orden sugerido):** contrato inicial de `kernel.v1` (2), resolución de actor y espacio (3 y 5), MCP solo lectura (1), validación Clerk (6), identidad del primer ingreso (4), llaves (7), webhooks: despacho, reintentos y secreto (8 y 9), proceso programador (13).

**Bloqueantes de alcance para devolver al dueño:** ninguno impide empezar la Parte A. Las que cambian alcance si se responden distinto son 1 (MCP), 12 (staging con web o no) y 13 (si expiran compromisos en staging); el resto tiene valor por defecto razonable.

**Cuentas externas necesarias solo para R11 y R12:** Clerk (claves de desarrollo), Supabase (staging), Dokploy (servidor), DNS de `nexusthrive.com.co`; Vercel solo si el dueño amplía el alcance en la pregunta 12.
