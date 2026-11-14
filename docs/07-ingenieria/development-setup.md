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

### Primeros pasos

```bash
git clone https://github.com/NexusThrive/nexusthrive.git
cd nexusthrive
cp .env.example .env      # completar las llaves de Clerk
task setup                # dependencias de Go y pnpm (instala según pnpm-lock.yaml)
task infra:up             # PostgreSQL 17 y Redis 7 (espera a que PostgreSQL esté listo)
task db:migrate           # crea el esquema kernel y aplica sus migraciones
task gen                  # código desde contracts/
task test                 # todas las pruebas
task dev                  # API y apps en modo desarrollo
```

### Comandos frecuentes

| Comando | Qué hace |
|---|---|
| `task lint` | Analiza Go (`gofmt`, `go vet`, `staticcheck`), TypeScript y contratos (`buf lint`) |
| `task lint:go`, `task lint:ts`, `task lint:contracts` | Cada análisis por separado; el CI usa estas |
| `task test:go`, `task test:ts` | Cada suite por separado |
| `task docs:check` | Dibuja todos los diagramas de `docs/` para detectar errores |
| `pnpm storybook` | Abre la biblioteca de componentes |
| `task db:migrate` | Crea el esquema `kernel` y aplica las migraciones pendientes; repetirla no cambia nada |
| `task infra:down` | Detiene la infraestructura local |

### Migraciones

Las migraciones son SQL puro en `kernel/db/migrations/` (ADR-0024) y se aplican con goose desde un comando Go del kernel. `task db:migrate` solo lo invoca; sin Task el equivalente es:

```bash
DATABASE_URL="postgres://nexusthrive:nexusthrive@localhost:55432/nexusthrive?sslmode=disable" \
  go run github.com/nexusthrive/nexusthrive/kernel/db/cmd/migrate
```

- El PostgreSQL local publica el puerto 55432 (configurable con `NEXUSTHRIVE_POSTGRES_PORT`) para no chocar con un PostgreSQL ya instalado en el 5432.
- `DATABASE_URL` tiene por defecto la base de `infra/docker/compose.yaml`. Una migración inválida termina con código distinto de 0 y el mensaje nombra el archivo.
- Contra Supabase usa la conexión directa o el pooler en modo sesión, nunca el modo transacción (ADR-0019).
- Las pruebas de integración de `kernel/db` leen la misma variable `DATABASE_URL` (con ese valor por defecto) y fallan si PostgreSQL no responde; ejecuta `task infra:up` antes. Cada prueba usa un esquema `kernel_test_<id>` que elimina al terminar.
- `task infra:up` espera al healthcheck de PostgreSQL, así que `task db:migrate` puede ir justo después.

### Herramientas que se descargan solas

`staticcheck` y `buf` se ejecutan con `go run` en la versión fijada en `Taskfile.yml`; no hace falta instalarlas. Go, Node.js, pnpm y Task sí deben estar instalados.

## Reglas e invariantes

- Nadie usa datos reales de clientes en su máquina.
- El entorno local debe funcionar solo con `.env.example` completado; si algo extra hace falta, se documenta aquí.
