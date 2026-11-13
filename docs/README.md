# Documentación de NexusThrive

## Propósito

Explicar qué es NexusThrive, cómo está construido y cómo se trabaja en él.

## Alcance

Se lee en orden. Cada carpeta responde una pregunta y tiene su propio índice.

| # | Carpeta | Responde |
|---|---|---|
| 00 | [Identidad](00-identidad/README.md) | ¿Qué es NexusThrive y qué no es? |
| 01 | [Modelo](01-modelo/README.md) | ¿Con qué piezas describe cualquier ecosistema? ¿Qué leyes nunca rompe? |
| 02 | [Arquitectura](02-arquitectura/README.md) | ¿Cómo está construido? Kernel, plataforma, datos, contrato, stack y hoja de ruta |
| 03 | [Distribuciones](03-distribuciones/README.md) | ¿Cómo se aplica a un mundo concreto? Primero: Thrive Business |
| 04 | [Integraciones](04-integraciones/README.md) | ¿Cómo se conecta con otras apps, la IA y los avisos? |
| 05 | [Decisiones](05-decisiones/README.md) | ¿Qué decidimos y por qué? (ADR) |
| 06 | [Reglas](06-reglas/README.md) | ¿Qué reglas mantienen el orden? |
| 07 | [Ingeniería](07-ingenieria/README.md) | ¿Cómo se escribe código, documentación, commits y pull requests? |

## Conceptos principales

Todo lo que se escribe aquí cae en una de tres capas, y nunca se mezclan:

- **Visión:** lo que NexusThrive llegará a ser.
- **Fundamento:** lo que debe existir para que la visión sea posible.
- **Implementación:** lo que se programa ahora, según la [hoja de ruta](02-arquitectura/hoja-de-ruta.md).

> La visión atrae, el fundamento ordena y la implementación paga.

## Reglas e invariantes

- Toda la documentación está en español; los nombres técnicos se mantienen en inglés.
- Cada página sigue el [estándar de documentación](07-ingenieria/documentation-standard.md).
- Los diagramas están en Mermaid y se validan con `task docs:check`. Para editarlos a mano: copiar el bloque y pegarlo en Excalidraw con *Insertar → Mermaid a Excalidraw*.
