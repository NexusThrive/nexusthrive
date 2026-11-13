# Plan de producto

Funcionalidad: Núcleo del kernel (M1 = B2). Backlog: `docs/product/backlog/nucleo-del-kernel.md`. Prioridad de todo el conjunto: Must.

## Sprint y contexto

- Sprint: `sprint/S01`, rama base de integración. Cada historia sale de `feature/HU-kernel-<tema>-NN` (igual a su ID) y se integra por PR en `sprint/S01`. Un PR por historia, con `docs/` actualizado.
- Tareas previas de documentación ya fusionadas en `sprint/S01` (contexto, no son historias y no entran en el orden): ADR-0017 a ADR-0023 y ADR-0025 (Redis diferido hasta que existan varias instancias de la API).
- ADR que escriben historias de este sprint (el ADR se commitea antes del código de la historia):
  - ADR-0024, herramienta de migraciones: lo escribe HU-kernel-migrations-01.
  - ADR-0026, código tipado `idempotency_conflict`: lo escribe HU-kernel-idempotency-14, junto con la actualización de ADR-0004 y de `docs/02-arquitectura/contrato.md`.

## Resumen de estado

| Concepto | Cantidad |
|---|---|
| Historias | 15 |
| Listas | 15 |
| Bloqueadas | 0 |
| En curso | 0 |

Tamaños: S = 1, M = 10, L = 4. P1 y P2 están resueltas en el backlog.

## Orden de ejecución

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

## Dependencias

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

## Ruta crítica y paralelismo

Ruta crítica (10 historias, 1 S + 4 L + 5 M): 01 -> 02 -> 03 -> 04 -> 07 -> 08 -> 11 -> 13 -> 14 -> 15. Cualquier retraso en estas mueve el cierre de M1. Las tres L (03, 08, 11) más 13 concentran el tiempo.

Pueden ir en paralelo (con ramas distintas hacia `sprint/S01`):

- Tras 03: 04 y 09.
- Tras 04: 05 y 07 (y 09 y 10 si siguen en curso).
- Tras 07: 08 mientras 05 -> 06 avanza.
- Cadena de holgura: 05 -> 06 (debe terminar antes de empezar 13) y 09 -> 10 (antes de 12 y 13).
- Tras 11: 12 mientras se prepara 13 si 06, 09 y 10 ya están.

Secuenciales obligatorias: 01 -> 02 -> 03 al inicio y 13 -> 14 -> 15 al final.

## Módulos compartidos (riesgo de conflicto)

| Módulo | Historias que lo tocan | Coordinación |
|---|---|---|
| Directorio de migraciones del esquema `kernel` | 01 a 14 (cada una añade tablas) | Numeración de archivos: reservar el orden antes de abrir ramas paralelas; el segundo PR rebasa y renumera. |
| Helper de transacción con `SET LOCAL kernel.space_id` y roles (`kernel/db`, `kernel/state`) | 03 (lo crea), todas las demás lo usan, 13 lo envuelve | 03 se fusiona antes de arrancar 04 y 09; no cambiar su firma sin avisar. |
| Arnés de pruebas de integración | 02 (lo crea), todas | Estable desde 02; los cambios van en PR propio. |
| Catálogo de permisos | 06 (lo define), 10, 11, 12, 13 (lo usan) | 06 fija las 13 claves; las historias previas no inventan claves. |
| Paquete de eventos y outbox | 09, 10, 12, 13 | Interfaz de escritura de evento + outbox definida en 09/10 y consumida por 12 y 13. |
| Tablas `exchanges` y `entries` | 08, 11 (`commitment_id` en asientos), 14 (`idempotency_key`) | 11 añade la columna en su migración; 14 no cambia el esquema de `exchanges`. |
| Errores tipados y `docs/02-arquitectura/contrato.md` | 03, 05, 06, 14 | 14 añade `idempotency_conflict`; evitar ediciones simultáneas del archivo. |
| `docs/02-arquitectura/kernel.md` | 12 (nota sin programador), 15 (nota sin programador y acceso del staff) | Una sola redacción; 15 revisa que no se contradiga. |
| `docs/02-arquitectura/hoja-de-ruta.md` y `docs/07-ingenieria/testing.md` | 02 | Solo 02 los edita. |
| `kernel/architecture_test.go` | todas (debe seguir en verde) | Ningún identificador con vocabulario de negocio; revisar en cada PR. |
| `.github/workflows/ci.yml` | 01 (job de migraciones), 02 (servicio PostgreSQL) | Fusionar 01 y 02 en orden; no editar en paralelo. |

## Bloqueos y acciones requeridas

No hay historias bloqueadas. Acciones de seguimiento, no bloqueantes:

| Acción | Quién | Cuándo |
|---|---|---|
| Aprobar el catálogo mínimo de 13 permisos | Responsable del producto, en la revisión del PR de HU-kernel-permissions-06 | Antes de fusionar 06 (y antes de empezar 13) |
| Objetar o aceptar los supuestos A1 a A6 y las reglas nuevas marcadas en el backlog (límite de 5 intentos del outbox, un participante por `(space_id, identity_id)`) | Responsable del producto, en los PR respectivos | En el PR de cada historia |
| Aceptar ADR-0024 antes de cualquier código de migración | Responsable del producto | Inicio de 01 |
| Aceptar ADR-0026 (y enmienda de ADR-0004) antes del código de idempotencia | Responsable del producto | Inicio de 14 |

Inconsistencia a corregir por el Requirements Analyst: la tabla "Verificación de Definition of Ready" del backlog aún marca "Sin TBD = pendiente" en HU-kernel-timers-12 y HU-kernel-idempotency-14, mientras sus estados dicen Lista porque P1 y P2 están resueltas. Además, los encabezados de sus criterios dicen "se confirman al desbloquear". Este plan las trata como listas por indicación del equipo; el analista debe actualizar la tabla y los encabezados.

## Riesgos

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

## Primeros tres IDs a tomar

1. HU-kernel-migrations-01 (empezar por ADR-0024).
2. HU-kernel-testinfra-02 (en cuanto 01 se fusione en `sprint/S01`; hasta entonces se puede preparar el flujo de CI).
3. HU-kernel-spaces-03 (en cuanto 02 se fusione).

Las tres son secuenciales: ninguna se puede paralelizar antes de que la anterior esté fusionada. El primer punto de paralelismo real llega al fusionar 03 (04 y 09).
