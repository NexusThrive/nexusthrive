# NexusThrive

**El kernel que estructura ecosistemas y los hace funcionar solos.**

NexusThrive registra lo que pasa, cuida lo que se promete y actúa con IA dentro de reglas claras. No sabe qué es un negocio, una comunidad o una persona: estructura lo que le llega. Encima de él viven las distribuciones, que sí hablan el idioma de cada mundo.

- **Nexus:** el núcleo que conecta.
- **Thrive:** prosperar. Generar, conservar y hacer crecer valor.

## Qué hay en este repositorio

| Carpeta | Qué contiene | Lenguaje |
|---|---|---|
| [`docs/`](docs/) | Identidad, modelo, arquitectura, decisiones y reglas | Markdown |
| [`contracts/`](contracts/) | El idioma común entre todas las piezas | Protobuf + JSON Schema |
| [`kernel/`](kernel/) | El núcleo: modelo, estado, permisos, coordinación, ejecución, extensiones | Go |
| [`distributions/`](distributions/) | Los mundos que corren sobre el kernel. Primero: `business` | YAML + Go |
| [`experience/`](experience/) | Las caras: web, móvil, escritorio, componentes | TypeScript + React |
| [`integrations/`](integrations/) | Avisos y conexiones con otras apps | n8n |

## Por dónde empezar

1. [Manifiesto](docs/00-identidad/manifiesto.md): qué es NexusThrive.
2. [Modelo universal](docs/01-modelo/primitivas.md): las siete piezas.
3. [Visión general de la arquitectura](docs/02-arquitectura/vision-general.md).
4. [Thrive Business](docs/03-distribuciones/business/README.md): la primera distribución.
5. [Reglas de construcción](docs/06-reglas/construccion.md): cómo trabajamos.

## Regla de oro

**Ninguna pieza le habla a otra sino por el contrato del kernel.**
