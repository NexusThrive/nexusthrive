# Reporte del sprint S01

Estado: EN CURSO

Fase: M1 (B2), núcleo del kernel. Backlog: `docs/product/backlog/nucleo-del-kernel.md`.

| Historia | Estado | PR | Intentos | Nota |
| --- | --- | --- | --- | --- |
| HU-kernel-migrations-01 | terminada | #10 | 1 | goose v3.26.0 y pgx v5.7.5 fijados para conservar Go 1.24. PostgreSQL local en el puerto 55432. Pruebas e implementación van en un mismo commit porque la compuerta del commit exige pruebas en verde. |
| HU-kernel-testinfra-02 | en revisión | #11 | 1 | Arnés `kernel/db/dbtest`. Pruebas e implementación van en un mismo commit porque la compuerta del commit exige verde. `task docs:check` no se pudo verificar por Chrome en el entorno. |

Preparación del ciclo: PR #9 (compuerta local).
