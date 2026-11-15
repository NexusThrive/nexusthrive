# Plan de producto

Este plan tiene una sección por sprint. S02 (M2, B3) va primero; S01 (M1, B2) se conserva como historial.

- [Sprint S02: las puertas del kernel (M2 = B3)](#sprint-s02-las-puertas-del-kernel-m2--b3)
- [Sprint S01: núcleo del kernel (M1 = B2), historial](#sprint-s01-núcleo-del-kernel-m1--b2-historial)

---

# Sprint S02: las puertas del kernel (M2 = B3)

Funcionalidad: `docs/product/funcionalidades/m2-puertas-del-kernel.md`. Backlog: `docs/product/backlog/m2-puertas-del-kernel.md` (HU-gateway-01 a 23). Prioridad de todo el conjunto: Must.

## Sprint y contexto (S02)

- Rama base de integración: `sprint/S02`. Una rama por historia (`feature/HU-gateway-NN`, o `chore/HU-gateway-NN` en las de solo documentación), un PR por historia, con `docs/` actualizado.
- Las historias 01 a 07 escriben los ADR 0036 a 0044 (números propuestos; el dueño los confirma en el PR). Ninguna historia de código empieza antes de que su ADR esté Aceptado (`AGENTS.md`, regla 12).
- Restricción de ejecución: los agentes comparten un solo árbol de trabajo. Dos ramas no pueden estar desprotegidas a la vez, así que "paralelo" aquí significa trabajar en archivos distintos sin cambiar de rama a mitad de trabajo y con commits que incluyan solo las rutas de la historia. Todo lo que comparte archivos va en serie.

## Resumen de estado (S02)

Actualización 2026-11-15: santy401 aceptó el [ADR-0037](../05-decisiones/0037-contrato-inicial-del-kernel.md) de HU-gateway-01 en `chore/HU-gateway-01`; quedan pendientes la validación completa y la integración de la historia. HU-gateway-08 tiene su decisión aceptada y es la siguiente historia de código habilitada. El ADR-0036 es la primera puerta HTTP aceptada; los números de ADR del plan original son reservas históricas y se reasignan al redactar cada historia. Los conteos siguientes reflejan este avance; las discrepancias del backlog descritas más abajo corresponden a su resumen original.

Validación local de HU-gateway-01: enlaces relativos y bloques de código comprobados, `git diff --check` sin errores. `task lint` pasó con Go 1.24.7 (el Staticcheck fijado no admite Go 1.27 del servidor) y `task test` pasó, incluidas las leyes del kernel contra PostgreSQL en 55432. Se ejecutó Task 3.40.1 mediante `go run` porque no está instalado como comando. Mermaid pasó con `CI=1`: 58 diagramas renderizados. La decisión está en el commit `9f19a48`; su integración sigue pendiente.

HU-gateway-08 está implementada y validada localmente en `feature/HU-gateway-08`, commit `387b33e`, sobre la decisión aceptada. Incluye contratos, esquema del evento, generación local, SDK generado, pruebas del contrato y CI de compatibilidad y regeneración. No incluye las implementaciones del motor ni del servidor. Lint, pruebas completas, typecheck, compilación y render de 58 diagramas pasaron. La integración espera la cadena previa: PR #27 y HU-gateway-01; no se ha fusionado ni abierto un PR nuevo.

HU-gateway-02 está en curso en `chore/HU-gateway-02`, con los ADR [0038](../05-decisiones/0038-resolucion-de-actor-y-espacio.md) (actor y directorio por slug) y [0039](../05-decisiones/0039-identidad-en-el-primer-ingreso.md) (primer ingreso) propuestos. HU-gateway-09 espera la aceptación de ambos antes de implementar el motor. La numeración antigua 0037/0038 para estos temas queda sustituida por 0038/0039; las reservas posteriores se reasignan al redactar cada historia.

| Concepto | Cantidad |
|---|---|
| Historias | 23 |
| Listas | 18 |
| Bloqueadas | 2 (HU-gateway-22 por Q1, HU-gateway-23 por Q2) |
| Terminadas / en curso | 0 / 3 (incluye historias pendientes de revisión o integración) |

De las 18 listas, 03, 07, 14, 15, 19 y 21 aparecen en el plan original con un supuesto por confirmar (S-P1, S-P13); el traspaso registra que el responsable ya confirmó ambos. Sus ADR siguen pendientes.

Discrepancias del backlog (se rutean al analista, no se corrigen aquí):

- El resumen del backlog dice 20 listas y 3 bloqueadas, pero solo 22 y 23 están marcadas "Bloqueada". Cuenta real: 21 y 2.
- El resumen dice S = 5, M = 10, L = 8. Contando los campos de tamaño: S = 8 (03, 04, 05, 07, 09, 18, 21, 22), M = 11 (01, 02, 06, 08, 11, 12, 14, 16, 17, 19, 20), L = 4 (10, 13, 15, 23).

## Orden de ejecución (S02)

Todas son Must. El orden lo fijan los bloqueos, luego la ruta crítica y a igual nivel el tamaño (menor primero). Ninguna historia cruza niveles de prioridad.

Fase 0, decisiones (solo `docs/`):

1. HU-gateway-01 (M): contrato y política de `buf`; gatea 08, la cabeza de la ruta crítica. Resuelve el choque del espacio de nombres `kernel`.
2. HU-gateway-02 (M): actor y espacio; gatea 09 y 12.
3. HU-gateway-06 (M): despacho y secretos de webhooks; gatea 15 (L, ruta crítica). Va antes que 05 porque 13 no está en la ruta hacia 18.
4. HU-gateway-07 (S) (*): programador; gatea 15 y 21.
5. HU-gateway-04 (S): Clerk; gatea 11 (y el ADR 0040 que 10 declara).
6. HU-gateway-03 (S) (*): MCP de solo lectura; gatea 14 y 19.
7. HU-gateway-05 (S): llaves; gatea 13, 14 y 19. Va después de 06 (comparten `modelo-de-datos/`).

Fase 1, ruta crítica de código (en serie: tocan `contracts/`, `services/api/cmd/api/main.go`, `kernel/gateway` y `.github/workflows/ci.yml`):

8. HU-gateway-08 (M): contratos y `task gen`.
9. HU-gateway-09 (S): operaciones y composición del `Engine`.
10. HU-gateway-10 (L): gateway Connect-RPC y JSON/HTTP.

Fase 1b, fuera de la ruta crítica pero necesaria para 12:

11. HU-gateway-11 (M): validación JWT; solo necesita 04 y es un paquete nuevo, así que puede avanzar mientras 08 a 10 están en curso (ver paralelismo).

Fase 2, cierre de B3:

12. HU-gateway-15 (L): webhooks firmados; mayor tamaño tras 10, por eso va antes que 12.
13. HU-gateway-12 (M): actor desde token y espacio por ruta.
14. HU-gateway-18 (S): criterio de B3 de extremo a extremo.

Fase 3, resto (con 18 ya en verde):

15. HU-gateway-21 (S) (*): ciclo de timers y despachador (tras 15, misma zona de código).
16. HU-gateway-20 (M): imagen, migración al arrancar y salud.
17. HU-gateway-16 (M): reintentos y deshabilitación (tras 15, mismo paquete).
18. HU-gateway-17 (M): reenvío y rotación (tras 16, mismo paquete).
19. HU-gateway-13 (L): llaves por espacio.
20. HU-gateway-14 (M) (*): MCP de solo lectura.
21. HU-gateway-19 (M) (*): nodo n8n.
22. HU-gateway-22 (S): bloqueada por Q1.
23. HU-gateway-23 (L) (*): bloqueada por Q2.

Razón de dejar 13 después de 18: ninguna de las cuatro condiciones de 18 usa llaves; 13 es L y solo gatea 14 y 19. Si hay capacidad extra, 13 puede subir después de 12 sin afectar la ruta crítica.

## Dependencias (S02)

Bloqueos explícitos de los campos "Dependencias" y de los ADR exigidos (bloquea a):

```
01 -> 08, 09, 10, 14, 19, 18
02 -> 09, 10, 12
04 -> 11, 10 (ADR 0040)
03 -> 14, 19
05 -> 13, 14, 19
06 -> 15, 16, 17, 18
07 -> 15, 21
08 -> 09, 10, 19
09 -> 10, 15
10 -> 12, 13, 14, 15, 18, 20
11 -> 12, 18, 22
12 -> 13, 18, 22
13 -> 14, 19
15 -> 16, 17, 18, 19, 21
20 -> 23
21 -> 23 (solo si S-P13 se confirma)
Q1 -> 22
Q2 -> 23
```

Verificación: sin ciclos y sin dependencias hacia historias inexistentes (todas las referencias están en 01 a 23). Huecos de consistencia para el analista:

- HU-gateway-10 exige el ADR 0040 (04), pero su campo Dependencias no lista 04. El plan lo trata como bloqueo.
- HU-gateway-19 exige los ADR 0039 y 0041 (03 y 05), pero sus Dependencias solo listan 08, 13 y 15. 05 llega por 13; 03 no llega por ninguna otra vía.
- HU-gateway-15 (AC9) y HU-gateway-21 (AC2) describen ambas el despachador en el binario de la API. Falta decir cuál posee el código del despachador y cuál el ciclo. El plan asume que 15 posee el despachador y 21 el ciclo que lo invoca junto con `FireDueTimers`.

## Ruta crítica hacia el criterio de B3 (HU-gateway-18)

Ruta crítica: 01 -> 08 -> 09 -> 10 -> 15 -> 18 (6 historias: 3 M, 1 S, 1 L, 1 S... en detalle 01 M, 08 M, 09 S, 10 L, 15 L, 18 S).

Ramas que deben terminar antes de 18 sin estar en la cadena:

- 02 antes de 09 (y de 12).
- 06 y 07 antes de 15.
- 04 -> 11 -> 12, y 12 necesita también 10. Es la segunda cadena: 12 (M) corre después de 10 y tiene holgura frente a 15 (L), siempre que 11 y 02 estén listas cuando 10 se fusione.

Si 11 se retrasa hasta después de 10, 12 y 18 se alargan y la ruta crítica pasa por 04 -> 11 -> 12 -> 18. Mitigación: empezar 11 en cuanto 04 sea Aceptado.

Fuera de la ruta crítica de B3: 13, 14, 16, 17, 19, 20, 21, 22, 23.

## Paralelismo y serie (S02)

Se pueden trabajar a la vez (archivos disjuntos, mismo árbol, sin cambiar de rama a mitad):

- Fase 0: 01, 02, 04, 07 son disjuntas entre sí salvo por el índice de ADR (ver módulos compartidos). 03 solo toca ADR-0018, ADR-0020, `AGENTS.md` y `puertas.md`, también disjunta. 
- 11 (paquete nuevo de validación JWT) con 08, 09 o 10, con una sola precaución: ambas tocan `go.mod` y `go.sum` (y `go.work.sum`), así que la segunda en fusionar rebasa.
- 20 con 16 o 17 (20 toca `services/api/Dockerfile` y arranque; 16 y 17 el paquete de webhooks). Aun así ambas tocan `main.go`.
- 14 y 19 entre sí (MCP generado frente a `integrations/n8n/`), una vez 13 y 15 estén fusionadas.

Deben ir en serie (comparten archivos):

- 05 y 06: ambas editan `docs/02-arquitectura/modelo-de-datos/`.
- 01 y 08: 08 edita `contrato.md`, que 01 también toca; 08 necesita el ADR de todos modos.
- 08, 09, 10, 12: `contracts/`, `services/api/cmd/api/main.go`, `kernel/gateway`, `kernel/architecture_test.go`.
- 10 y 12 y 13: la composición de autenticación en `kernel/gateway` y `main.go`.
- 15, 16, 17 y 21: paquete de despacho de webhooks y despachador; 16 y 17 se pueden solapar solo si 15 ya está fusionada y editan archivos distintos del paquete; por defecto, en serie.
- 13 y 15: ambas añaden migraciones (ver riesgos).
- 08 y 20: ambas editan `.github/workflows/ci.yml`; un PR a la vez.

## Módulos compartidos (riesgo de conflicto) (S02)

| Módulo | Historias | Coordinación |
|---|---|---|
| `docs/05-decisiones/README.md` (índice de ADR) y numeración 0036 a 0044 | 01 a 07 | Reservar los números ya (01: 0036; 02: 0037 y 0038; 03: 0039; 04: 0040; 05: 0041; 06: 0042 y 0043; 07: 0044). Una sola persona actualiza el índice al cerrar la fase 0, o cada PR rebasa el índice. |
| `docs/02-arquitectura/modelo-de-datos/` | 05, 06, 13, 15 | 05 y 06 en serie; los cambios de 13 y 15 llegan con su migración. |
| `docs/02-arquitectura/contrato.md` | 01, 08 | 01 lo toca primero (quita Python); 08 después. |
| `docs/04-integraciones/puertas.md` | 03, 10 (tabla de códigos), 16 (cifras), 17 | Serie por orden de fases. |
| `docs/02-arquitectura/flujos.md` | 06, 16 | 06 fija la cifra; 16 solo verifica que coincida. |
| `contracts/` y `task gen` | 08 (lo crea), 09, 10, 13, 15, 17, 19 | 08 se fusiona antes de cualquier otra; los cambios al contrato después van en el PR de la historia que los necesita y pasan `buf breaking`. |
| `services/api/cmd/api/main.go` | 09, 10, 12, 13, 14, 15, 20, 21 | Punto de contención principal: cada historia añade una línea de cableado; serie, rebase antes de fusionar. |
| `kernel/gateway` y `kernel/architecture_test.go` | 10, 12, 13, 14, 15, 17, 21 | Una sola regla de puerta (solo `Engine`, sin `kernel/state`); revisar la prueba en cada PR. |
| `kernel/execution` (operaciones propias `kernel*`, `validateOperation`, `Run`) | 01 (decisión), 09, 12, 15, 21 | Si el ADR 0036 cambia el motor, ese cambio entra en 09 y no se mezcla con otras. |
| Directorio de migraciones (desde 00015) | 13, 15 | Ambas dicen "00015 o superior". Reservar números por historia al tomarla; la segunda renumera. |
| `.github/workflows/ci.yml` y `Taskfile` | 08 (buf, gen), 20, 22 (`task smoke:clerk`), 23 | Un PR a la vez. |
| `go.mod`, `go.sum`, `go.work.sum` | 08, 11, 13, 14, 15, 19 | La segunda historia que añade dependencias rebasa. Nota: `go.work.sum` ya aparece sin seguimiento en el árbol de trabajo. |
| `AGENTS.md`, ADR-0018, ADR-0020 | 03 | Solo 03 los edita. |

## Bloqueos y acciones requeridas (S02)

Historias bloqueadas:

| Historia | Bloqueo | Qué la desbloquea | Quién | Para cuándo |
|---|---|---|---|---|
| HU-gateway-22 | Q1: credenciales de desarrollo de Clerk (instancia, `iss`, `azp`, clave pública); además 11 y 12 fusionadas | Entregar una instancia de desarrollo de Clerk y que 11 y 12 estén fusionadas | Dueño (Q1) | Antes de tomar 22, al fusionar 12 |
| HU-gateway-23 | Q2: Supabase de staging, servidor Dokploy, DNS de `nexusthrive.com.co`; además 20 y, si S-P13 se confirma, 21 fusionadas | Cuentas y accesos como secretos de GitHub; 20 y 21 fusionadas | Dueño (Q2) | Antes de tomar 23, al fusionar 20 |

Ninguna de las dos está en la ruta crítica de B3. Las dos pueden esperar sin retrasar 18.

Decisiones que necesita el dueño (no bloquean el arranque):

| Acción | Quién | Cuándo |
|---|---|---|
| Aceptar cada ADR (0036 a 0044) y confirmar su número | Dueño, en cada PR de las historias 01 a 07 | Antes de empezar la historia de código que lo exige; 0036 y 0037 son los más urgentes (gatean 08 y 09) |
| Confirmar S-P1 (MCP solo lectura) | Dueño | En el PR de 03, antes de 14 y 19 |
| Confirmar S-P12 (staging sin Vercel) | Dueño | Antes de tomar 23 |
| Confirmar S-P13 (programador en el binario de la API) | Dueño | En el PR de 07, antes de 15 y 21 |
| Aceptar los valores nuevos fijados por ADR (esperas de reintento, ventana de doble secreto, límite de renovación de JWKS, intervalo del ciclo) | Dueño | En el PR del ADR respectivo (04, 06, 07) |

Rutas de vuelta:

- A Requirements Analyst: las tres discrepancias de consistencia (conteo y tamaños, 10 sin 04, 19 sin 03, propiedad del despachador entre 15 y 21) y la superposición de números de migración entre 13 y 15.
- A Product Owner: S-P1, S-P12 y S-P13 si el dueño los rechaza (cambia el alcance).

## Supuestos por confirmar del dueño y su efecto

| Supuesto | Historias afectadas | Si se rechaza |
|---|---|---|
| S-P1 (*): MCP solo de lectura | 03, 14, 19 | 03 deja de ser una enmienda y pasa a decidir la escritura con propuestas aprobadas; 14 crece (herramientas de `write`, aprobación humana) y probablemente pasa a XL; 19 cambia su paridad. Vuelve al Product Owner y al analista. |
| S-P12 (*): staging sin Vercel | 23 | 23 crece (web, consola, Vercel, Redis, dominios `staging.` y `staging-console.`) y deja de caber en M2. No toca la ruta de B3. |
| S-P13 (*): programador en el binario de la API | 07, 15, 21 (y 06 en su cláusula del despachador, 18 en el criterio 3, 23 por la dependencia de 21) | 21 se pospone a M4 y en staging los compromisos vencidos no expiran. 07 debe decir quién despacha, y 15 y 18 deben definir cómo corre el despachador. Es el supuesto con más efecto sobre la ruta crítica, porque 15 depende del ADR 0044. |

## Riesgos (S02)

| Riesgo | Probabilidad | Impacto | Evidencia | Mitigación |
|---|---|---|---|---|
| R8, webhooks: 15, 16 y 17 concentran una L y dos M, con migraciones, cifrado reversible, validación de destinos privados, reparto entre espacios y tres cifras de reintento en conflicto | Alta | Alto: 15 está en la ruta crítica y 16, 17 y 21 cuelgan de él | Backlog: R8 es XL en la definición y se partió en 15, 16 y 17; ADR-0031 (quinto fallo), `puertas.md` (20 fallos) y `flujos.md` (hasta 8) dan cifras distintas; los valores de espera y de ventana son nuevos | Cerrar 06 primero; terminar 15 antes de abrir 16 y 17; reloj inyectable en las pruebas; 18 depende solo de 15 para no esperar a 16 y 17 |
| Choque del espacio de nombres `kernel` en HU-gateway-01: `validateOperation` reserva `kernel` y `Run` rechaza todo nombre que empiece por `kernel`, pero las operaciones propuestas son `kernel.exchange.write` y `kernel.event.read` | Alta | Alto: si el ADR obliga a cambiar el motor, 09 crece y 08, 09 y 10 se retrasan; si se renombran, cambia el contrato | `kernel/execution/operation.go` y `kernel/execution/engine.go`, según la nota del backlog | 01 AC4 resuelve esto antes del código; aceptar 0036 antes de 08; si exige cambiar `execution`, abrir ese cambio como parte de 09 o PR previo con su prueba de ley. |
| Plugins remotos de `buf` en CI: 08 pide `task gen` sin red y con plugins locales fijados | Media | Alto: sin generación estable no hay 08 y todo lo posterior se detiene | 08 AC3 y AC6; ADR 0036 AC3 (plugins locales y fijados); P10 | Empezar 08 con una prueba de humo del `task gen` en CI desde el primer commit; fijar versiones de `buf`, `protoc-gen-go`, `protoc-gen-connect-go` y el plugin de TypeScript en `tools/`; comprobar que un segundo `task gen` no cambia nada. |
| Un solo árbol de trabajo para todos los agentes provoca commits cruzados, rebases forzados y pruebas ejecutadas sobre cambios ajenos | Alta | Medio | Restricción indicada para S02; `go.work.sum` y archivos sin seguimiento ya presentes en el árbol | Ejecutar en serie lo que comparte archivos; cada commit con rutas explícitas; no cambiar de rama con trabajo sin confirmar; fase 0 en lotes disjuntos. |
| El dueño debe aceptar nueve ADR antes del código; la lentitud de su respuesta detiene la ruta crítica | Media | Alto | Backlog: ninguna historia de código empieza antes de ADR Aceptado y los números son propuestos | Presentar 01, 02, 06 y 07 primero y juntos; 04, 03 y 05 en un segundo lote; el plan marca qué historia espera cada ADR. |
| Colisión de migraciones de 13 y 15 (ambas "desde 00015") | Alta | Medio | ADR 0041 AC3 y 13 AC1; 15 AC1 | Reservar rangos al tomar cada historia; pasar el hueco al analista. |
| Dependencias de 10 y 19 con ADR no listadas en Dependencias | Media | Bajo a medio | Campos "ADR exigidos" frente a "Dependencias" de 10 y 19 | Tratar 04 como bloqueo de 10 y 03 como bloqueo de 19 (ya hecho en este plan); el analista corrige las historias. |
| El rechazo de S-P13 deja sin dueño al despachador y rompe 18 AC3 | Baja a media | Alto | S-P13 aparece en 07, 15, 21; 18 AC3 exige que "el despachador corra" | Si se rechaza, el ADR 0044 debe decir quién despacha; la prueba de 18 puede invocar el despachador directamente sin pasar por 21. |
| Pruebas de 13, 15 y 18 inestables por tiempo real y concurrencia | Media | Medio | Backlog pide reloj inyectable para 10 y 16; 15 criterio 7 (rotación entre espacios) | Reloj inyectable y repeticiones, como en S01; ninguna prueba depende del reloj real. |

## Primeros tres IDs a tomar (S02)

1. HU-gateway-01 (escribir ADR-0036 primero; resuelve el espacio de nombres `kernel`).
2. HU-gateway-02 (ADR-0037 y 0038).
3. HU-gateway-06 (ADR-0042 y 0043).

Las tres son docs-only, tocan archivos disjuntos entre sí (salvo el índice de ADR) y gatean las tres cabezas de cadena: 08, 09 y 15. Pueden trabajarse a la vez en el mismo árbol si cada commit lleva solo sus rutas. Siguen 07 y 04 (pequeñas), luego 03 y 05. Cuando 01 esté Aceptado empieza 08.

---

# Sprint S01: núcleo del kernel (M1 = B2), historial

Funcionalidad: Núcleo del kernel (M1 = B2). Backlog: `docs/product/backlog/nucleo-del-kernel.md`. Prioridad de todo el conjunto: Must.

### Sprint y contexto

- Sprint: `sprint/S01`, rama base de integración. Cada historia sale de `feature/HU-kernel-<tema>-NN` (igual a su ID) y se integra por PR en `sprint/S01`. Un PR por historia, con `docs/` actualizado.
- Tareas previas de documentación ya fusionadas en `sprint/S01` (contexto, no son historias y no entran en el orden): ADR-0017 a ADR-0023 y ADR-0025 (Redis diferido hasta que existan varias instancias de la API).
- ADR que escriben historias de este sprint (el ADR se commitea antes del código de la historia):
  - ADR-0024, herramienta de migraciones: lo escribe HU-kernel-migrations-01.
  - ADR-0026, código tipado `idempotency_conflict`: lo escribe HU-kernel-idempotency-14, junto con la actualización de ADR-0004 y de `docs/02-arquitectura/contrato.md`.

### Resumen de estado

| Concepto | Cantidad |
|---|---|
| Historias | 15 |
| Terminadas | 15 |
| Bloqueadas | 0 |
| En curso | 0 |

Tamaños: S = 1, M = 10, L = 4. P1 y P2 están resueltas en el backlog.

### Orden de ejecución

Todas son Must, así que el orden lo fijan los bloqueos y, a igual nivel, el tamaño (menor primero). Ninguna historia cruza niveles de prioridad.

1. HU-kernel-migrations-01 (M): raíz de todo; sin migraciones no hay tablas ni pruebas de integración.
2. HU-kernel-testinfra-02 (M): arnés contra PostgreSQL real que usan todas las demás.
3. HU-kernel-spaces-03 (L): RLS y `SET LOCAL`; toda tabla posterior depende de este patrón.
4. HU-kernel-participants-04 (M): desbloquea grants y recursos. Va antes que events-09 porque está en la ruta crítica.
5. HU-kernel-events-09 (M): solo necesita spaces-03; va en paralelo con 04 si hay segunda persona.
6. HU-kernel-resources-07 (S): pequeña y en la ruta crítica; sigue a 04.
7. HU-kernel-grants-05 (M): fuera de la ruta crítica; en paralelo con 07.
8. HU-kernel-exchanges-08 (L): sigue a 07; ruta crítica.
9. HU-kernel-outbox-10 (M): sigue a 09.
10. HU-kernel-permissions-06 (M): sigue a 05; debe estar antes de 13.
11. HU-kernel-commitments-11 (L): necesita 04, 07 y 08; ruta crítica.
12. HU-kernel-timers-12 (M): necesita 11, 09 y 10.
13. HU-kernel-execution-13 (L): integra 06, 08, 09, 10 y 11; ruta crítica.
14. HU-kernel-idempotency-14 (M): sigue a 13; escribe ADR-0026 primero.
15. HU-kernel-laws-15 (M): cierre; necesita las 14 anteriores.

### Dependencias

```
01 -> 02 -> 03 -> 04 -> 05 -> 06 -> 13
              03 -> 09 -> 10 -> 12
              03 -> 07 (también 04 -> 07) -> 08 -> 11 -> 12
              04 -> 11
              06, 08, 09, 10, 11 -> 13 -> 14
              01..14 -> 15
```

Por historia (bloqueada por):

- 01: ninguna. 02: 01. 03: 02. 04: 03. 05: 04. 06: 05.
- 07: 03, 04. 08: 07. 09: 03. 10: 09.
- 11: 04, 07, 08. 12: 11, 09, 10.
- 13: 06, 08, 09, 10, 11. 14: 13. 15: 01 a 14.

Verificación: sin ciclos y sin dependencias hacia historias inexistentes. Observación: el diagrama de "Orden y dependencias" del backlog resume 11 como "07 + 04"; el campo Dependencias de HU-kernel-commitments-11 agrega 08 (los cumplimientos usan asientos). Este plan usa el campo de la historia. 12 no bloquea a 13; solo bloquea a 15.

### Ruta crítica y paralelismo

Ruta crítica (10 historias, 1 S + 4 L + 5 M): 01 -> 02 -> 03 -> 04 -> 07 -> 08 -> 11 -> 13 -> 14 -> 15. Cualquier retraso en estas mueve el cierre de M1. Las tres L (03, 08, 11) más 13 concentran el tiempo.

Pueden ir en paralelo (con ramas distintas hacia `sprint/S01`):

- Tras 03: 04 y 09.
- Tras 04: 05 y 07 (y 09 y 10 si siguen en curso).
- Tras 07: 08 mientras 05 -> 06 avanza.
- Cadena de holgura: 05 -> 06 (debe terminar antes de empezar 13) y 09 -> 10 (antes de 12 y 13).
- Tras 11: 12 mientras se prepara 13 si 06, 09 y 10 ya están.

Secuenciales obligatorias: 01 -> 02 -> 03 al inicio y 13 -> 14 -> 15 al final.

### Módulos compartidos (riesgo de conflicto)

| Módulo | Historias que lo tocan | Coordinación |
|---|---|---|
| Directorio de migraciones del esquema `kernel` | 01 a 14 (cada una añade tablas) | Numeración de archivos: reservar el orden antes de abrir ramas paralelas; el segundo PR rebasa y renumera. |
| Helper de transacción con `SET LOCAL kernel.space_id` y roles (`kernel/db`, `kernel/state`) | 03 (lo crea), todas las demás lo usan, 13 lo envuelve | 03 se fusiona antes de arrancar 04 y 09; no cambiar su firma sin avisar. |
| Arnés de pruebas de integración | 02 (lo crea), todas | Estable desde 02; los cambios van en PR propio. |
| Catálogo de permisos | 06 (lo define), 10, 11, 12, 13 (lo usan) | 06 fija las 13 claves; las historias previas no inventan claves. |
| Paquete de eventos y outbox | 09, 10, 12, 13 | Interfaz de escritura de evento + outbox definida en 09/10 y consumida por 12 y 13. |
| Tablas `exchanges` y `entries` | 08, 11 (cumplimientos en `fulfillments`), 14 (`idempotency_key`) | 11 no añade `commitment_id` a `entries` (ADR-0033: `fulfillments` es la única fuente de verdad); 14 no cambia el esquema de `exchanges`. |
| Errores tipados y `docs/02-arquitectura/contrato.md` | 03, 05, 06, 14 | 14 añade `idempotency_conflict`; evitar ediciones simultáneas del archivo. |
| `docs/02-arquitectura/kernel.md` | 12 (nota sin programador), 15 (nota sin programador y acceso del staff) | Una sola redacción; 15 revisa que no se contradiga. |
| `docs/02-arquitectura/hoja-de-ruta.md` y `docs/07-ingenieria/testing.md` | 02 | Solo 02 los edita. |
| `kernel/architecture_test.go` | todas (debe seguir en verde) | Ningún identificador con vocabulario de negocio; revisar en cada PR. |
| `.github/workflows/ci.yml` | 01 (job de migraciones), 02 (servicio PostgreSQL) | Fusionar 01 y 02 en orden; no editar en paralelo. |

### Bloqueos y acciones requeridas

No hay historias bloqueadas. Acciones de seguimiento, no bloqueantes:

| Acción | Quién | Cuándo |
|---|---|---|
| Aprobar el catálogo mínimo de 13 permisos | Responsable del producto, en la revisión del PR de HU-kernel-permissions-06 | Antes de fusionar 06 (y antes de empezar 13) |
| Objetar o aceptar los supuestos A1 a A6 y las reglas nuevas marcadas en el backlog (límite de 5 intentos del outbox, un participante por `(space_id, identity_id)`) | Responsable del producto, en los PR respectivos | En el PR de cada historia |
| Aceptar ADR-0024 antes de cualquier código de migración | Responsable del producto | Inicio de 01 |
| Aceptar ADR-0026 (y enmienda de ADR-0004) antes del código de idempotencia | Responsable del producto | Inicio de 14 |

Corregida: la tabla "Verificación de Definition of Ready" del backlog marca HU-kernel-timers-12 y HU-kernel-idempotency-14 como listas ("sí" en "Sin TBD"), porque P1 y P2 están resueltas, y el plan las trata como listas.

### Riesgos

| Riesgo | Probabilidad | Impacto | Mitigación |
|---|---|---|---|
| Cuatro historias L en la cadena crítica (03, 08, 11, 13) alargan el sprint | Media | Alto: retrasa 15 y el inicio de B3 | Empezar 03 apenas cierre 02; no abrir historias sin dependencias cumplidas; revisar el avance al terminar cada L. |
| Conflictos de numeración de migraciones y de edición de `contrato.md` y `kernel.md` en ramas paralelas (backlog: casi todas las historias añaden migraciones) | Alta | Medio | Ver "Módulos compartidos"; rebase antes de fusionar; un PR a la vez sobre ci.yml. |
| Mecanismo RLS con `SET LOCAL` mal diseñado en 03 obliga a rehacer tablas posteriores (A1, criterio 6 de 03) | Media | Alto | Las pruebas de 03 incluyen pool de una sola conexión y rol sin `BYPASSRLS`; no iniciar 04 ni 09 hasta fusionar 03. |
| La prueba de arquitectura solo ve identificadores Go; vocabulario de negocio puede colarse en SQL o cadenas de prueba (backlog, laws-15) | Media | Medio | Revisión con búsqueda de términos en cada PR; la revisión formal queda en 15. |
| Ningún proceso dispara timers en producción hasta una fase posterior (decisión del responsable, timers-12) | Alta | Medio: compromisos no vencen solos | Documentarlo en `kernel.md` (12 y 15) y registrarlo como entrada de B3 o posterior. |
| CI sin servicio PostgreSQL estable provoca pruebas intermitentes o omitidas (`t.Skip` prohibido, testinfra-02) | Media | Alto | 02 incluye fallo explícito si falta PostgreSQL; la prueba de guardia de 15 detecta omisiones. |
| El catálogo de permisos (06) cambia tras aprobación y afecta 10 a 13 | Baja | Medio | Aprobar en el PR de 06 antes de empezar 13; las claves se importan de un único lugar. |
| La concurrencia (outbox 10, timers 12, idempotencia 14) produce pruebas inestables | Media | Medio | Los criterios piden repeticiones (20 en outbox) y reloj inyectable; no usar tiempo real. |

### Primeros tres IDs a tomar

1. HU-kernel-migrations-01 (empezar por ADR-0024).
2. HU-kernel-testinfra-02 (en cuanto 01 se fusione en `sprint/S01`; hasta entonces se puede preparar el flujo de CI).
3. HU-kernel-spaces-03 (en cuanto 02 se fusione).

Las tres son secuenciales: ninguna se puede paralelizar antes de que la anterior esté fusionada. El primer punto de paralelismo real llega al fusionar 03 (04 y 09).
