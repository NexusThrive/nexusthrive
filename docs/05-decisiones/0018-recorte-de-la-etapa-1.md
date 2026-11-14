# ADR-0018 · Recorte de la etapa 1

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

La etapa 1 (B1 a B6) describe una base completa. Para vender el MVP de tienda no hace falta toda; sí hace falta que cada fase termine con un criterio verificable. Sin un recorte explícito, cada fase crece hasta cubrir casos que nadie usa todavía.

## Decisión

Cada fase B se entrega en su **versión MVP** y lo pospuesto queda listado aquí. Una fase MVP se da por terminada con pruebas automáticas en CI, igual que antes (regla 11 de `AGENTS.md`: solo lo que la fase necesita).

| Fase | Entra en el MVP | Pospuesto |
|---|---|---|
| B2 | Primitivas, cinco leyes, espacios, identidad y permisos, libro, compromisos, eventos con outbox, migraciones | Particionado del libro, reportes de ledger a gran escala |
| B3 | API Connect-RPC y JSON/HTTP, Clerk, idempotencia, credenciales con alcances, MCP (lectura y escritura controlada), webhooks firmados, nodo de n8n generado | MCP con recursos y prompts avanzados, SDK en otros lenguajes |
| B4 | Espacios con prueba, planes como datos, suscripción, cobro con Wompi, cobranza, piel Tienda versionada, equipo con roles, correos | Cobro recurrente con tarjeta guardada, facturas por correo con PDF, múltiples monedas de cobro |
| B5 | Negocios, planes y precios, suscripciones y cobros, soporte auditado, entregas de webhooks, credenciales, AURI del equipo | Paneles de analítica de la plataforma, edición masiva |
| B6 | Pruebas de aislamiento, doble factor del equipo, copias y restauración probada, monitoreo, límites de tasa, cabeceras, rotación de secretos, privacidad y términos | Auditoría externa, pruebas de caos, restauración periódica automatizada |

Lo que sale de la etapa 1 y entra al MVP de Business se describe en [mvp](../03-distribuciones/business/mvp/README.md). Lo que esté pospuesto aquí no se construye hasta que un ADR nuevo lo reactive.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Construir cada fase completa | Retrasa la venta sin aportar al primer cliente. |
| Saltar B5 (consola) y operar por scripts | El equipo no podría cobrar, soportar ni auditar sin herramientas; contradice la regla de que la plataforma opera todo. |

## Consecuencias

### Positivas

- Cada fase tiene un alcance cerrado y un criterio verificable.
- Lo pospuesto no se pierde: queda listado.

### Negativas

- Habrá deuda técnica consciente que pagar después de la v1.0.

### Riesgos

- Que "MVP" se use para recortar seguridad. Mitigación: B6-MVP conserva aislamiento, doble factor y restauración probada.

## Cumplimiento

- La hoja de ruta enlaza este ADR y repite el criterio de cada fase.
- Al abrir cada PR de sprint se revisa que no haya entrado nada de la columna "Pospuesto".

## Referencias

- [Hoja de ruta](../02-arquitectura/hoja-de-ruta.md)
- [ADR-0017](0017-mvp-web-tienda-primero.md)
