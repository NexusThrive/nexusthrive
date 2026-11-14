# Decisiones de arquitectura

## Propósito

Registrar cada decisión importante con su contexto, sus alternativas y sus consecuencias.

## Alcance

Todas las decisiones que afectan la arquitectura, los datos, los contratos, los lenguajes o las reglas del proyecto.

## Funcionamiento

- Cada decisión usa la [plantilla](plantilla.md).
- Se numeran en orden y nunca se reutiliza un número.
- Una decisión aceptada no se edita: se reemplaza con una nueva.
- Estados: `Propuesta`, `Aceptada`, `Reemplazada por ADR-NNNN`, `Obsoleta`.

## Registro

| # | Decisión | Estado |
|---|---|---|
| [0001](0001-kernel-sin-mundo.md) | El kernel no conoce ningún mundo | Aceptada |
| [0002](0002-lenguajes-por-capa.md) | Go en el kernel, TypeScript en las caras y Python para análisis | Aceptada |
| [0003](0003-monorepo.md) | Un solo repositorio con estructura de monorepo | Aceptada |
| [0004](0004-contrato-unico.md) | Un contrato único del que se genera todo | Aceptada |
| [0005](0005-integraciones.md) | Lo crítico va directo; lo demás, por n8n | Aceptada |
| [0006](0006-identidad.md) | Clerk autentica; el kernel autoriza | Aceptada |
| [0007](0007-configura-nexusthrive.md) | El dueño no activa funciones | Aceptada |
| [0008](0008-patrones-y-pieles.md) | Patrones con código, pieles con configuración | Aceptada |
| [0009](0009-libro-de-movimientos.md) | Libro de movimientos de doble partida como fuente de la verdad | Aceptada |
| [0010](0010-esquemas-por-capa.md) | Un esquema de base de datos por capa | Aceptada |
| [0011](0011-plataforma-por-encima.md) | La plataforma está por encima de los espacios, con acceso auditado | Aceptada |
| [0012](0012-politica-de-idiomas.md) | Política de idiomas | Aceptada |
| [0013](0013-internacionalizacion.md) | Interfaz internacionalizada | Aceptada |
| [0014](0014-estandar-de-documentacion.md) | Estándar de documentación y ADR | Aceptada |
| [0015](0015-pieles-versionadas.md) | Pieles versionadas con despliegue controlado | Aceptada |
| [0016](0016-sistema-de-diseno.md) | Sistema de diseño en un paquete de marca y licencia de fuentes registrada | Aceptada |
| [0017](0017-mvp-web-tienda-primero.md) | MVP web: Tienda primero | Aceptada |
| [0018](0018-recorte-de-la-etapa-1.md) | Recorte de la etapa 1 | Aceptada |
| [0019](0019-alojamiento-y-entornos.md) | Alojamiento y entornos | Aceptada |
| [0020](0020-control-externo.md) | Control externo: API, MCP, webhooks y n8n | Aceptada |
| [0021](0021-auri.md) | AURI, la IA de NexusThrive | Aceptada |
| [0022](0022-cobro-con-wompi.md) | Cobro de NexusThrive con Wompi | Aceptada |
| [0023](0023-urls-y-dominios.md) | URLs y dominios | Aceptada |
| [0024](0024-herramienta-de-migraciones.md) | Goose como herramienta de migraciones en SQL puro | Aceptada |
| [0025](0025-redis-diferido.md) | Redis diferido hasta que haya varias instancias de la API | Aceptada |
| [0027](0027-roles-y-rls.md) | Roles de base de datos y RLS con SET LOCAL | Aceptada |
| [0028](0028-orden-de-eventos.md) | Eventos de solo agregar: inmutabilidad en dos capas y orden de `seq` | Aceptada |
| [0029](0029-otorgante-entre-espacios-y-escalada.md) | Otorgante entre espacios y reglas contra la escalada de privilegios | Aceptada |
| [0030](0030-libro-de-doble-partida.md) | Libro de doble partida: conservación diferida, inmutabilidad en dos capas, compensación y decimales | Aceptada |
| [0031](0031-outbox-reclamo-y-arrendamiento.md) | Outbox: reclamo de un solo espacio, arrendamiento, entrega al menos una vez y valores de partida | Aceptada |
| [0032](0032-catalogo-de-permisos-y-verificacion.md) | Catálogo de permisos y verificación: orden de errores, bloqueo y límite del actor afirmado | Aceptada |
| [0033](0033-compromisos-estado-derivado-y-asiento-unico.md) | Compromisos: estado derivado, concurrencia por bloqueo de fila, monto cumplido verificado y cumplimientos inmutables | Aceptada |
| [0034](0034-vencimiento-de-compromisos-por-timer.md) | Vencimiento de compromisos por timer: `expired` solo por cascada desde un timer disparado, con el reloj de la base como techo | Aceptada |

## Pendientes

| Tema | Bloquea |
|---|---|
| Licencia comercial de n8n para usar credenciales de clientes | Fase N3 |
| Crear las cuentas de Render, Supabase, Vercel y DNS del dominio (ADR-0019) | Despliegue de staging (M2) |
| Confirmar precios y límites provisionales de los planes (ADR-0022) | Wompi en producción |
| Lista de tiendas piloto | Piloto |
