# ADR-0019 · Alojamiento y entornos

- **Estado:** Propuesta
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

Hay que alojar cuatro piezas: la API en Go (contenedor Docker), PostgreSQL con copias, la web estática (`apps/web`, `apps/console` y Storybook) y n8n. Los usuarios están en Colombia y el equipo es pequeño, por lo que se prefiere poco trabajo de operación y poco costo fijo. Ya existe un servidor propio con Dokploy y n8n en marcha, y el dominio `nexusthrive.com.co`.

Esta decisión tiene **costo mensual** (Render y Supabase en production), por lo que sigue en `Propuesta` hasta que una persona la acepte. Regiones, planes y precios cambian: deben confirmarse al contratar.

## Decisión

Hay **una sola API** (una imagen Docker) desplegada en dos entornos. El reparto es:

| Pieza | `staging` | `production` |
|---|---|---|
| Web y consola | Vercel (vista previa) | Vercel |
| API (`services/api`) | Servidor propio con Dokploy, desplegada automáticamente desde `main` | Render, por etiqueta `vX.Y.Z` con aprobación manual |
| PostgreSQL | Proyecto de Supabase propio | Proyecto de Supabase propio, con plan de pago y copias |
| n8n | Servidor propio con Dokploy | La misma instancia, con credenciales separadas por entorno |
| Storybook | Vercel o servidor propio | — |

El entorno `local` se levanta con `task infra:up`.

Reglas comunes:

- **Supabase se usa solo como PostgreSQL administrado.** La API de datos (PostgREST) y Supabase Auth se desactivan: Clerk autentica y solo el kernel y sus extensiones tocan la base (ADR-0006, ADR-0010).
- Un proyecto de Supabase por entorno; nunca comparten base, claves ni cuentas de Clerk y Wompi.
- La API se conecta de forma directa o por el pooler en modo sesión; el modo transacción no se usa porque rompe las sentencias preparadas de `pgx`.
- La base de production se crea en la misma región que la API de production para evitar latencia entre continentes.
- La infraestructura se describe como código dentro de `infra/`. Las migraciones corren en cada despliegue antes de recibir tráfico.
- Los secretos viven en el gestor de cada proveedor, nunca en el repositorio.
- Subdominios de `nexusthrive.com.co`: `app`, `console`, `api`, `staging`, `staging-api` y `storybook`.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Todo en Render (API, base, web, n8n) | Un solo panel, pero más costo fijo y sin aprovechar el servidor propio ya disponible. |
| Producción en el servidor propio | Internet residencial, un solo disco y cortes de energía no sirven para cobrar a clientes. |
| Fly.io | PostgreSQL administrado más reciente y más operación manual. |
| Google Cloud (Cloud Run y Cloud SQL) | Región en São Paulo, pero más piezas que operar y costo base mayor para un MVP. |
| Base en el servidor propio para production | Sin copias externas ni alta disponibilidad. |
| Web en Cloudflare Pages | Plan gratuito apto para uso comercial; queda como reemplazo si el plan de Vercel resulta limitante. |

## Consecuencias

### Positivas

- Staging sin costo y con despliegue automático.
- Production con base administrada, copias y restauración.
- Migrar a otro proveedor es posible: todo es contenedor y PostgreSQL estándar.

### Negativas

- Dos plataformas de despliegue de la API (Dokploy y Render) que mantener equivalentes.
- El plan gratuito de Supabase pausa proyectos inactivos: staging puede tardar en despertar.

### Riesgos

- El plan gratuito de Vercel (Hobby) es para uso no comercial: production requiere el plan de pago. Confirmar los términos vigentes antes de lanzar.
- Staging y n8n dependen de un servidor residencial: si cae, se atrasan avisos y despliegues de prueba, nunca dinero (ADR-0005).
- Datos personales de personas en Colombia alojados fuera del país: requiere revisión legal (Ley 1581 de 2012) antes de producción. El texto de privacidad se marca `needs human review`.
- Exposición accidental del servidor propio: `nexus-postgres` y otros servicios no deben escuchar fuera de la red local o de Tailscale; la entrada pública va por `cloudflared`.
- Licencia de n8n: la instancia propia es para operar NexusThrive; ofrecer n8n a terceros exige revisar su licencia (pendiente de ADR-0005).

## Cumplimiento

- `infra/` contiene la definición de `staging` y `production`.
- Una prueba de humo corre tras cada despliegue contra `/healthz` y `/readyz`.
- Una prueba de arquitectura impide que el código use endpoints de la API de datos de Supabase.
- La restauración de una copia se prueba antes de la v1.0 (fase B6).

## Referencias

- [Despliegue](../02-arquitectura/despliegue.md)
- [ADR-0006](0006-identidad.md)
- [ADR-0010](0010-esquemas-por-capa.md)
