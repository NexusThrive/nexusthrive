# Reporte del sprint S01

Estado: EN CURSO

Fase: M1 (B2), núcleo del kernel. Backlog: `docs/product/backlog/nucleo-del-kernel.md`.

| Historia | Estado | PR | Intentos | Nota |
| --- | --- | --- | --- | --- |
| HU-kernel-migrations-01 | terminada | #10 | 1 | goose v3.26.0 y pgx v5.7.5 fijados para conservar Go 1.24. PostgreSQL local en el puerto 55432. Pruebas e implementación van en un mismo commit porque la compuerta del commit exige pruebas en verde. |
| HU-kernel-testinfra-02 | terminada | #11 | 1 | Arnés `kernel/db/dbtest`. Pruebas e implementación van en un mismo commit porque la compuerta del commit exige verde. `task docs:check` no se pudo verificar por Chrome en el entorno. |
| HU-kernel-spaces-03 | terminada | #12 | 1 | ADR-0027 (roles y RLS). Migraciones 00001 a 00003; la numeración siguiente parte de 00004. Criterio 5 se lee como en la nota de diseño: el UPDATE de una fila de otro espacio afecta 0 filas; cambiar `id` a otro espacio falla con 42501. Supuesto A7 (formato de `slug`) y `permission_denied` para actores distintos de `system`. `task docs:check` no se pudo verificar localmente por Chrome. |
| HU-kernel-participants-04 | terminada | #13 | 1 | Enmienda fechada del ADR-0027 (`search_path` fijo, guarda de cinco atributos, políticas por comando en tablas sin `space_id`, riesgo aceptado de vincular una identidad ajena). Migraciones 00004 (endurecimiento) y 00005 (identidades y participantes); la numeración siguiente parte de 00006. Supuestos: `display_name` obligatorio, tipo y número de documento juntos, `country_code` de dos mayúsculas, `UPDATE` por columnas, `permission_denied` para actores distintos de `system`, índice único parcial `(space_id, identity_id)`. `task docs:check` no se pudo verificar localmente por Chrome. |
| HU-kernel-events-09 | terminada | #14 | 2 | ADR-0028 (inmutabilidad en dos capas y orden de `seq`). Migración 00006; la numeración siguiente parte de 00007. Lecturas del responsable: `type` de dos o más segmentos sin sufijo de versión (el ejemplo de `kernel.md` se corrigió), "desde `seq = n`" inclusivo, actor ausente es `permission_denied` en Go con el `CHECK` como respaldo, y `UNIQUE (id, space_id)` en `participants` para la clave foránea compuesta del actor. `task docs:check` no se pudo verificar localmente por Chrome. Requirió una segunda ronda por un hallazgo de seguridad (el `GRANT INSERT` sobre `seq` permitía fijarlo; se pasó a grants por columnas). |
| HU-kernel-resources-07 | terminada | #15 | 1 | Migración 00007; la numeración siguiente parte de 00008. Lecturas del responsable: `closed_at` en `accounts`, `name` de cuenta obligatorio, unicidad `(space_id, holder_participant_id, code)`, `scale` de 0 a 18 y `code` de recurso en mayúsculas, claves foráneas compuestas, cuenta cerrada que no se reabre (disparador). El rechazo de asientos en cuentas cerradas y la verificación completa de `amount` quedan en exchanges-08 (contrato en la nota de diseño). Dinero en Go diferido a exchanges-08. `task docs:check` no se pudo verificar localmente por Chrome. |


Preparación del ciclo: PR #9 (compuerta local).
