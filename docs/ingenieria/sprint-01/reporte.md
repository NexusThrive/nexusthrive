# Reporte del sprint S01

Estado: EN CURSO

Fase: M1 (B2), núcleo del kernel. Backlog: `docs/product/backlog/nucleo-del-kernel.md`.

| Historia | Estado | PR | Intentos | Nota |
| --- | --- | --- | --- | --- |
| HU-kernel-migrations-01 | terminada | #10 | 1 | goose v3.26.0 y pgx v5.7.5 fijados para conservar Go 1.24. PostgreSQL local en el puerto 55432. Pruebas e implementación van en un mismo commit porque la compuerta del commit exige pruebas en verde. |
| HU-kernel-testinfra-02 | terminada | #11 | 1 | Arnés `kernel/db/dbtest`. Pruebas e implementación van en un mismo commit porque la compuerta del commit exige verde. `task docs:check` no se pudo verificar por Chrome en el entorno. |
| HU-kernel-spaces-03 | terminada | #12 | 1 | ADR-0027 (roles y RLS). Migraciones 00001 a 00003; la numeración siguiente parte de 00004. Criterio 5 se lee como en la nota de diseño: el UPDATE de una fila de otro espacio afecta 0 filas; cambiar `id` a otro espacio falla con 42501. Supuesto A7 (formato de `slug`) y `permission_denied` para actores distintos de `system`. `task docs:check` no se pudo verificar localmente por Chrome. |
| HU-kernel-participants-04 | en revisión | #13 | 1 | Enmienda fechada del ADR-0027 (`search_path` fijo, guarda de cinco atributos, políticas por comando en tablas sin `space_id`, riesgo aceptado de vincular una identidad ajena). Migraciones 00004 (endurecimiento) y 00005 (identidades y participantes); la numeración siguiente parte de 00006. Supuestos: `display_name` obligatorio, tipo y número de documento juntos, `country_code` de dos mayúsculas, `UPDATE` por columnas, `permission_denied` para actores distintos de `system`, índice único parcial `(space_id, identity_id)`. `task docs:check` no se pudo verificar localmente por Chrome. |

Preparación del ciclo: PR #9 (compuerta local).
