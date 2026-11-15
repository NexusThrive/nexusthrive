# Traspaso: lo que falta de M1 y de la etapa 1

Documento para el agente que continúa el trabajo. Léelo entero, luego `AGENTS.md` y los documentos que este archivo cita. Si algo no está documentado, no lo inventes: pregunta al responsable (santy401).

## 1. Reglas que no se negocian

Resumen de `AGENTS.md`; el original manda.

- El kernel no aprende palabras de negocio (`customer`, `invoice`, `sale`, `membership`, `gym`, `restaurant`). Una prueba de arquitectura lo exige.
- `kernel/` nunca importa `platform/`, `distributions/` ni `services/`. Solo `kernel/state` y `kernel/execution` importan `kernel/state`. Las puertas reciben únicamente el `Engine`, nunca el pool.
- Una operación, una transacción. Libro, compromisos, eventos y outbox se escriben juntos o no se escriben.
- Conservación (los asientos de un intercambio suman cero por recurso) y memoria (eventos y asientos son de solo anexar; se corrige con intercambios compensatorios).
- Toda operación declara permiso, capacidad, riesgo (`read`, `write`, `money`) e idempotencia en `contracts/`. Dinero siempre idempotente. Dinero con decimales exactos, nunca `float`.
- Decisión primero: un cambio significativo exige un ADR en `docs/05-decisiones/` antes del código.
- Pruebas antes que la implementación. `kernel/laws/` nunca se omite. Antes de abrir un PR: `task lint`, `task test`, `task docs:check`. En esta máquina `task` y Chrome no funcionan; CI es la fuente de verdad de `docs:check`.
- Rama: `^(feature|fix|chore|hotfix)/HU-[a-z0-9]+(-[a-z0-9]+)*-[0-9]{2}$`, por ejemplo `feature/HU-gateway-contracts-01`. Las ramas de integración son `sprint/S<nn>`. Nunca se commitea en `main`. Un PR por historia con `.github/pull_request_template.md` y `docs/` actualizado en el mismo cambio.
- Commits: Conventional Commits en inglés. Autor `santy401 <santy401@users.noreply.github.com>`. **Nunca** `Co-Authored-By`, firmas, enlaces ni menciones de herramientas o asistentes en commits, PR o archivos.
- Idioma: `docs/` y `README.md` raíz en español; código, comentarios, logs, errores y pruebas en inglés; texto al usuario por `@nexusthrive/i18n` (`es` por defecto).
- Entorno: la PostgreSQL de pruebas es el contenedor del proyecto en el puerto **55432**. Nunca toques el 5432 (es de otros servicios del servidor del responsable). El rol `nexusthrive_api` es global del clúster: no le des `LOGIN`. El hook del plugin bloquea comandos de Bash con `|` y `;`, y su aviso de "illegal character U+0023" sobre `.md` es un falso positivo.

## 2. Dónde está cada cosa

- Estado: `main` contiene M1 completo (PR #26 fusionado). `sprint/S02` está creada desde `main` para M2.
- Rama con trabajo sin subir: `feature/HU-gateway-first-door-01` (2 commits locales sobre `sprint/S02`, sin push ni PR). Trae `kernel/gateway`, `Engine.ResolveActor`, `Tx.ListBalances`, el comando `kernel/state/cmd/seed`, `tools/demo/run.sh` y el ADR-0036 (aceptado).
- Informe de M1: `docs/ingenieria/sprint-01/reporte.md`. Seguimientos con su destino: `docs/ingenieria/sprint-01/seguimientos.md` (79 filas, tabla de destinos arriba).
- Backlog y plan de M2: `docs/product/backlog/m2-puertas-del-kernel.md`, `docs/product/plan.md`, `docs/product/funcionalidades/m2-puertas-del-kernel.md`, `docs/product/contexto-dominio.md`.
- ADR 0017 a 0036 en `docs/05-decisiones/`. Notas de diseño de M1 en `docs/ingenieria/diseno/`.

## 3. Decisiones del responsable ya tomadas

- S-P1: el MCP es solo de lectura. Hay que enmendar los ADR-0018 y 0020, que dicen lectura y escritura controlada, para que coincidan con `AGENTS.md` y `puertas.md`.
- S-P12: "staging en línea" incluye API, base, n8n y DNS; no incluye Vercel, web ni consola.
- S-P13: el programador de temporizadores y el despachador del outbox corren dentro del binario de la API.
- ADR-0036 aceptado: puerta en `kernel/gateway`, espacio por id en la ruta, token RS256, operaciones en el espacio de nombres `ledger` para el corte inicial.

## 4. Lo que falta de M1 (seguimientos abiertos)

Numeración = columna `#` de `seguimientos.md`. Léela ahí antes de actuar: el texto completo de cada fila explica el porqué.

### 4.1 Decisiones del responsable que piden un ADR (bloquean código)

Pregúntale y escribe el ADR antes de tocar nada.

- **35**: ¿se prohíbe compensar una compensación? La inclinación documentada es prohibirla. Incluye exigir en la base `kind = kernel.exchange.reversal` cuando `reverses_exchange_id` no es nulo.
- **56**: ¿se endurece que un acreedor cumpla su propio compromiso (contra-asiento del deudor, posterior al compromiso)?
- **63 y 68**: política de retención de registros de idempotencia y de timers.
- **4, 58**: comportamiento fijado con prueba, decisión pendiente de cambiarlo.
- Lecturas que el responsable aún no confirma: cuerpos de los PR #12, #16 y #17 a #23, sección "Readings pending the owner's confirmation". Las más importantes: criterio 4 de grants-05 y el quinto fallo del outbox.
- PR #1 (cerrado): su descripción menciona una herramienta, algo que `AGENTS.md` prohíbe. Preguntar si se corrige.

### 4.2 Antes del staging

- **1**: la contraseña del rol de la API puede quedar en logs del servidor con `ALTER ROLE`. Calcular el verificador SCRAM en Go (ADR-0027). Una variable vacía no revoca un `LOGIN` ya concedido.
- **2**: probar un inicio de sesión real con el rol limitado (hoy las pruebas usan `SET ROLE`).

### 4.3 Para B3 (las puertas)

- **4, 6**: `IdentityID` nunca viene de un cliente; se resuelve en el servidor. Decidir cómo buscar o crear una identidad sin enumerarla (errores `23505`/`23503` revelan existencia) y con límite de tasa.
- **12, 13**: límite de tamaño del `payload` de eventos; los consumidores por cursor de `seq` reanudan con traslape y deduplican por `id`.
- **28, 29, 46, 47**: quién certifica al actor de plataforma. El `Actor` afirmado por la aplicación debe salir de un token validado; la API puede escribir `access_audit` y `grants` atribuyendo acciones a otro participante si conoce su UUID.
- **33, 64**: código de error reintentable en el contrato para `40001` y `40P01`.
- **39, 41, 42, 69, 70, 72**: despacho del outbox, reconciliar las cifras de reintento (el quinto fallo pasa a `dead`; 20 fallos seguidos deshabilitan un endpoint; "hasta 8" de `flujos.md` hay que corregirlo), no exponer `ClaimOutbox`, `MarkOutboxSent` ni `MarkOutboxFailed` a clientes, idempotencia en la puerta, `Call.Request`.

### 4.4 Para B4 (plataforma y soporte)

- **21**: tope de duración del acceso de soporte (solo se exige `expires_at`).
- **43**: reabrir filas `dead` del outbox desde la consola, con su propia migración.
- **47**: reactivar un espacio `suspended` solo con un actor de plataforma certificado.

### 4.5 Para B6 (endurecimiento)

- **2**: inicio de sesión real del rol limitado.
- **10**: `CREATE SCHEMA IF NOT EXISTS` corre antes del bloqueo consultivo; riesgo con varias instancias.
- **31**: documentar el procedimiento de restauración (`--disable-triggers`).
- **40**: auditoría de DDL (un superusuario puede apagar los disparadores del outbox).
- **43, 63, 68, 71**: retención y purga de outbox, timers e idempotencia.

### 4.6 Proceso programador

- **60, 61**: nada dispara temporizadores en producción. Con S-P13 el ciclo vive en el binario de la API (`FireDueTimers` y el despachador); es la historia `HU-gateway-21`.

### 4.7 ADR o decisión posterior

5 (reactivar participantes), 18 y 54 (cuentas cerradas o titulares archivados), 22 y 23 (capacidades suspendidas, grants vencidos), 30 (compensar con cuentas cerradas), 48 (orden de arranque de la primera distribución), 51 y 52 (compensación y compromisos cumplidos; cumplimiento parcial), 73.

### 4.8 Pulido y deuda de prueba (sin riesgo de seguridad)

8, 9, 14, 16, 25, 32, 34, 37, 38, 57, 65, 66, 67, 74 a 79. Atiéndelos al pasar por ese código; cada fila dice cuál es.

## 5. Lo que falta de la etapa 1

Orden de la hoja de ruta (`docs/02-arquitectura/hoja-de-ruta.md`).

### 5.1 B3, las puertas (M2)

Hecho: un corte HTTP con token RS256, `ResolveActor`, transferencia idempotente, saldos, eventos y una página de desarrollo. Pendiente, en el orden del plan (`docs/product/plan.md`):

1. Decisión primero (historias `HU-gateway-01` a `07`, solo `docs/`): contrato `kernel.v1` y `buf`, resolución del actor y del espacio por `slug`, MCP de solo lectura, validación de tokens de Clerk, llaves por espacio, despacho y secreto de webhooks, proceso programador. Hallazgo para el ADR del contrato: `validateOperation` reserva el espacio de nombres `kernel` y `Run` rechaza cualquier nombre que empiece por `kernel`, así que `kernel.exchange.write` y `kernel.event.read` chocan; decidir cómo se registran y cómo las alcanza la puerta.
2. Contratos en `contracts/` (hoy solo hay `buf.yaml`, `buf.gen.yaml` y READMEs; ningún `.proto` ni JSON Schema) y Connect-RPC. Los plugins de `buf` son remotos: confirmar que CI llega a esa red o fijarlos en `tools/`.
3. `kernel/gateway` sobre Connect-RPC, con errores tipados, `PanicError` traducido a respuesta genérica y límites de tasa.
4. Validación de tokens de Clerk con claims `iss`, `azp`, expiración, JWKS y caída del emisor (ya hay una base en `kernel/gateway/token.go`).
5. Llaves por espacio (migración desde `00015`; reservar el rango, `HU-gateway-13` y `15` lo reclaman ambas).
6. MCP de solo lectura.
7. Webhooks firmados en tres partes: firma y entrega, reintentos y deshabilitación a los 20 fallos, reenvío y rotación del secreto. El modelo guarda `secret_hash`, pero firmar con HMAC exige conocer el secreto: usar cifrado reversible (decisión por defecto del Product Owner).
8. Nodo de n8n generado, imagen Docker y salud, verificador SCRAM del rol de la API.
9. `HU-gateway-18`: prueba automática del criterio de B3 (token, operación, repetición idempotente, webhook firmado).
10. Bloqueadas por cuentas: `HU-gateway-22` (prueba con un token real de Clerk) y `23` (staging en línea).

### 5.2 B4, capa de plataforma

Personal de NexusThrive, planes, suscripciones, cobros, códigos de beneficio, versiones de pieles, solicitudes de capacidades y acceso de soporte temporal y auditado. Esquema `platform`. Fuentes: `docs/02-arquitectura/modelo-de-datos/plataforma.md`, `docs/02-arquitectura/plataforma/`.

### 5.3 B5, consola (`apps/console`)

Cliente sin lógica de negocio, que habla con el backend solo por `@nexusthrive/sdk`. Cada componente nace en Storybook con estados de carga, vacío, datos y error, y usa únicamente los tokens de `packages/brand`. Reglas de diseño: `docs/06-reglas/sistema-de-diseno`. La página embebida de `kernel/gateway/console.html` es solo de desarrollo y no la reemplaza.

### 5.4 B6, endurecimiento

Pruebas de aislamiento, MFA del personal, observabilidad, respaldo y restauración (ver 4.5).

## 6. Lo que solo puede hacer el responsable

- Subir `feature/HU-gateway-first-door-01` y abrir el PR hacia `sprint/S02` (o pedírselo al agente), y fusionar lo que corresponda a `main`.
- Reunir cuentas: Clerk (instancia de desarrollo con `iss`, `azp` y clave pública), dos proyectos de Supabase (staging y production), Vercel (y su plan comercial), acceso a Dokploy y el DNS de `nexusthrive.com.co`.
- Confirmar precios y límites provisionales de los planes (ADR-0022) y la lista de tiendas piloto.
- Las decisiones de la sección 4.1.

## 7. Cómo verificar el estado actual

```
go build ./...
cd kernel && go test ./...        # tarda unos minutos; requiere la PostgreSQL de infra en 55432
tools/demo/run.sh                 # demo local en http://127.0.0.1:18090/
```

Para el estado de cada seguimiento y cada historia, lo que dicen los archivos citados arriba manda sobre este resumen.
