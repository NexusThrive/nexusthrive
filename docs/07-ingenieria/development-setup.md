# Entorno de desarrollo

## Propósito

Preparar una máquina para trabajar en NexusThrive.

## Alcance

Herramientas, primeros comandos y estructura de trabajo local.

## Funcionamiento

### Herramientas

Versiones fijadas en `.tool-versions`:

| Herramienta | Versión |
|---|---|
| Go | 1.24 |
| Node.js | 22 |
| pnpm | 9 |
| Python | 3.12 |
| Docker | reciente, con Compose |
| [Task](https://taskfile.dev) | 3 |
| [Buf](https://buf.build) | reciente |

### Primeros pasos

```bash
git clone https://github.com/NexusThrive/nexusthrive.git
cd nexusthrive
cp .env.example .env      # completar las llaves de Clerk
task setup                # dependencias de Go y pnpm
task infra:up             # PostgreSQL 17 y Redis 7
task gen                  # código desde contracts/
task test                 # todas las pruebas
task dev                  # API y apps en modo desarrollo
```

### Comandos frecuentes

| Comando | Qué hace |
|---|---|
| `task lint` | Analiza Go, TypeScript y contratos |
| `task docs:check` | Dibuja todos los diagramas de `docs/` para detectar errores |
| `pnpm storybook` | Abre la biblioteca de componentes |
| `task infra:down` | Detiene la infraestructura local |

## Reglas e invariantes

- Nadie usa datos reales de clientes en su máquina.
- El entorno local debe funcionar solo con `.env.example` completado; si algo extra hace falta, se documenta aquí.
