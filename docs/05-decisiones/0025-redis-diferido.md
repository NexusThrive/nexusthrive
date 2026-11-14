# ADR-0025 · Redis diferido hasta que haya varias instancias de la API

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

[Despliegue](../02-arquitectura/despliegue.md) lista Redis como "administrado" para caché y sesiones, pero el [ADR-0019](0019-alojamiento-y-entornos.md) no define dónde corre. Las sesiones las maneja Clerk; la idempotencia, el outbox y los eventos viven en PostgreSQL por la ley de memoria. Lo único que justificaría Redis son los límites de tasa por credencial y la caché de lecturas pesadas, y con una sola instancia de la API se resuelven en memoria. Redis en el servidor propio, hablando por internet con una API en Render, añadiría latencia y un puerto expuesto.

## Decisión

- **Production:** sin Redis al inicio. Los límites de tasa por credencial viven en memoria de la API. Cuando haya más de una instancia, se añade un Redis administrado del proveedor de la API, con un ADR nuevo y su costo.
- **Staging:** Redis en el servidor propio con Dokploy, solo para probar el camino con Redis cuando exista.
- **Local:** el Redis de `task infra:up` se mantiene.
- **Regla de código:** el kernel y la API no dependen de Redis para correctitud. Nada que afecte dinero, existencias, idempotencia o permisos se guarda solo en Redis.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Redis administrado desde el primer día | Costo mensual sin necesidad mientras haya una sola instancia. |
| Redis en el servidor propio para production | API en Render y Redis en casa: latencia, puerto expuesto y punto de falla residencial. |
| Límites de tasa en PostgreSQL | Escribe en la base por cada petición; mala relación costo y beneficio. |

## Consecuencias

### Positivas

- Una pieza menos que pagar y operar en el lanzamiento.
- Correctitud independiente de Redis.

### Negativas

- Con varias instancias, los límites en memoria dejan de ser exactos hasta añadir Redis.

### Riesgos

- Que la API escale a varias instancias sin añadir Redis y los límites de tasa queden por instancia. Mitigación: se revisa al configurar el escalado en Render (fase B6).

## Cumplimiento

- Una prueba de arquitectura vigila que `kernel/` no importe clientes de Redis.
- La revisión de PR rechaza guardar solo en Redis datos que afecten dinero, existencias, idempotencia o permisos.

## Referencias

- [ADR-0006](0006-identidad.md)
- [ADR-0009](0009-libro-de-movimientos.md)
- [ADR-0019](0019-alojamiento-y-entornos.md)
- [Despliegue](../02-arquitectura/despliegue.md)
