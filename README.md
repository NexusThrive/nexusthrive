# NexusThrive

**El kernel que estructura ecosistemas y los hace funcionar solos.**

NexusThrive registra lo que pasa, cuida lo que se promete y actúa con IA dentro de reglas claras. No sabe qué es un negocio, una comunidad o una persona: estructura lo que le llega. Encima de él viven las distribuciones, que sí hablan el idioma de cada mundo, y por encima de todo está la plataforma con la que el equipo NexusThrive opera.

- **Nexus:** el núcleo que conecta.
- **Thrive:** prosperar. Generar, conservar y hacer crecer valor.

## Estructura

| Carpeta | Contenido | Lenguaje |
|---|---|---|
| [`kernel/`](kernel/) | El núcleo: modelo, estado, permisos, coordinación, ejecución, extensiones | Go |
| [`platform/`](platform/) | La capa por encima de todo: equipo, planes, suscripciones, pieles, soporte | Go |
| [`distributions/`](distributions/) | Los mundos sobre el kernel. Primero: `business` | Go + YAML |
| [`services/`](services/) | `api` (el binario) y `ai` (análisis) | Go, Python |
| [`apps/`](apps/) | `console`, `web`, `mobile`, `desktop` | TypeScript + React |
| [`packages/`](packages/) | `brand`, `ui`, `app`, `sdk`, `i18n`, `config` | TypeScript |
| [`contracts/`](contracts/) | El idioma común entre todas las piezas | Protobuf + JSON Schema |
| [`integrations/`](integrations/) | Avisos y conexiones con n8n | n8n |
| [`infra/`](infra/) · [`tools/`](tools/) | Infraestructura local y utilidades | YAML, Node |
| [`docs/`](docs/) | Toda la documentación | Español |

## Orden de construcción

1. **Etapa 1 · Base de NexusThrive:** kernel, puertas, plataforma y consola.
2. **Etapa 2 · Thrive Business:** base de negocios, gimnasio, tienda y restaurante.

Detalle en la [hoja de ruta](docs/02-arquitectura/hoja-de-ruta.md).

## Empezar

```bash
task setup && task infra:up && task test
```

Guía completa en [development-setup](docs/07-ingenieria/development-setup.md).

## Leer primero

1. [Manifiesto](docs/00-identidad/manifiesto.md)
2. [Modelo universal](docs/01-modelo/primitivas.md)
3. [Visión general de la arquitectura](docs/02-arquitectura/vision-general.md)
4. [Plataforma y consola](docs/02-arquitectura/plataforma/README.md)
5. [Estándar de ingeniería](docs/07-ingenieria/README.md)

Para contribuir: [CONTRIBUTING.md](CONTRIBUTING.md). Para agentes de código: [AGENTS.md](AGENTS.md).

## Regla de oro

**Ninguna pieza le habla a otra sino por el contrato del kernel.**
