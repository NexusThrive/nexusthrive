# ADR-0019 · Alojamiento y entornos

- **Estado:** Propuesta
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

Hay que alojar cuatro piezas: la API en Go (contenedor Docker), PostgreSQL administrado con copias, la web estática (`apps/web`, `apps/console`, Storybook) y n8n autoalojado. Los usuarios están en Colombia. El equipo es pequeño: se prefiere poco trabajo de operación. [Despliegue](../02-arquitectura/despliegue.md) listaba opciones sin elegir.

Esta decisión tiene **costo mensual**, por lo que queda en `Propuesta` hasta que una persona la acepte (ver puntos de control humano). Las características de cada proveedor, sus regiones y sus precios cambian: deben confirmarse en el momento de contratar.

## Decisión

Se propone **Render** para la API, PostgreSQL, n8n y los sitios estáticos, con estos entornos:

| Entorno | Cómo se despliega | Datos |
|---|---|---|
| `local` | `task infra:up` | Base y Redis locales |
| `staging` | Automático desde `main` | Base propia con datos de prueba |
| `production` | Por etiqueta `vX.Y.Z`, con aprobación manual | Base propia con copias y restauración probada |

Reglas comunes:

- La infraestructura se describe como código (blueprint del proveedor) dentro de `infra/`.
- Las migraciones corren en cada despliegue antes de recibir tráfico.
- Los secretos viven en el gestor del proveedor, nunca en el repositorio.
- Staging y production no comparten base, claves ni cuentas de Clerk y Wompi.

## Alternativas consideradas

| Alternativa | Ventajas | Por qué no se eligió |
|---|---|---|
| **Render** (elegida) | Docker, PostgreSQL administrado con copias, sitios estáticos y servicios con disco (n8n) en un solo panel; blueprint como código; poca operación | Sin región en Sudamérica a la fecha de esta propuesta (mayor latencia desde Colombia); ver riesgos |
| Fly.io | Regiones cercanas a Colombia, despliegue por contenedor | PostgreSQL administrado más reciente y más trabajo de operación; sitios estáticos y n8n requieren armarse a mano |
| Google Cloud (Cloud Run, Cloud SQL, Storage) | Región en São Paulo, escalado y copias maduros | Más piezas que configurar y operar (redes, IAM, balanceadores); costo base mayor para un MVP |
| VPS propio (Hetzner u otro) con Coolify | Costo bajo y control total | El equipo asumiría copias, parches y alta disponibilidad de PostgreSQL |

## Consecuencias

### Positivas

- Un solo proveedor y un solo panel para el MVP.
- Staging y production idénticos por blueprint.
- Migrar luego a AWS o GCP es posible: todo es contenedor y PostgreSQL estándar.

### Negativas

- Latencia mayor que con una región sudamericana.
- Dependencia de un proveedor para copias y restauración.

### Riesgos

- Datos personales de personas en Colombia alojados fuera del país: requiere revisión legal (Ley 1581 de 2012) antes de producción. Mitigación: el texto de privacidad y términos queda marcado `needs human review`.
- Licencia de n8n: la instancia propia es para operar NexusThrive; ofrecer n8n a terceros exige revisar su licencia (pendiente de [ADR-0005](0005-integraciones.md)).

## Cumplimiento

- `infra/` contiene el blueprint de `staging` y `production`.
- Una prueba de humo corre tras cada despliegue contra `/healthz` y `/readyz`.
- La restauración de una copia se prueba antes de la v1.0 (fase B6).

## Referencias

- [Despliegue](../02-arquitectura/despliegue.md)
- [ADR-0010](0010-esquemas-por-capa.md)
