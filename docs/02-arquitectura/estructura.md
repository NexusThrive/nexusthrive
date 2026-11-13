# Estructura del monorepo

## Propósito

Definir dónde vive cada pieza del código y qué dependencias están permitidas.

## Alcance

Incluye las carpetas raíz, su contenido, su lenguaje y sus reglas de dependencia.

## Conceptos principales

```
nexusthrive/
├── README.md               resumen (español)
├── AGENTS.md               guía para agentes de código (inglés)
├── CLAUDE.md               importa AGENTS.md
├── CONTRIBUTING.md         cómo contribuir (inglés)
├── CHANGELOG.md            cambios por versión (inglés)
│
├── kernel/                 Go · el núcleo
├── platform/               Go · la capa por encima de todo
├── distributions/
│   └── business/           Go + YAML · Thrive Business
├── services/
│   ├── api/                Go · el binario que une kernel, plataforma y distribuciones
│   └── ai/                 Python · análisis e IA
├── apps/
│   ├── console/            React · consola NexusThrive
│   ├── web/                React · app de los negocios
│   ├── mobile/             Expo · recepción
│   └── desktop/            Tauri · escritorio
├── packages/
│   ├── ui/                 componentes (Tailwind + Storybook)
│   ├── app/                lógica de pantallas compartida
│   ├── sdk/                cliente generado desde contracts
│   ├── i18n/               textos por idioma
│   └── config/             tsconfig, ESLint y Tailwind compartidos
├── contracts/              Protobuf + JSON Schema · fuente única
├── integrations/n8n/       nodo y flujos de n8n
├── infra/                  Docker local y despliegue
├── tools/                  utilidades del repositorio
├── docs/                   documentación (español)
├── .github/                plantillas y CI (inglés)
├── go.work                 módulos Go
├── package.json            raíz pnpm + Turborepo
├── pnpm-workspace.yaml
├── turbo.json
└── Taskfile.yml            comandos comunes: task dev, task test, task gen
```

## Funcionamiento

### Quién puede depender de quién

```mermaid
flowchart LR
    CON["contracts/"] -->|"genera"| GEN["código generado"]
    KER["kernel/"] --> GEN
    PLT["platform/"] -->|"API de extensión"| KER
    BUS["distributions/business/"] -->|"API de extensión"| KER
    API["services/api/"] --> KER
    API --> PLT
    API --> BUS
    APPS["apps/*"] --> PKG["packages/*"]
    PKG --> GEN
    INT["integrations/n8n/"] --> GEN
    KER -.-x|"prohibido importar"| PLT
    KER -.-x|"prohibido importar"| BUS
    BUS -.-x|"prohibido importar"| PLT
    APPS -.-x|"prohibido importar"| KER
```

- `services/api` es el único lugar que conoce a todos: arma el binario.
- `platform/` y las distribuciones nunca se importan entre sí; si necesitan algo común, va al kernel o al contrato.
- Las apps solo hablan con el backend por red, usando `@nexusthrive/sdk`.

### Por dentro del kernel

```mermaid
flowchart TB
    GW["gateway/<br/>API · MCP · webhooks"] --> CAP["capability/<br/>identidad, espacios, permisos"]
    CAP --> EXE["execution/<br/>operaciones, transacción, idempotencia"]
    EXT["extension/<br/>registro de extensiones"] --> EXE
    EXE --> MOD["model/<br/>las siete piezas"]
    EXE --> ST["state/<br/>libro, conservación, eventos"]
    ST --> CO["coordination/<br/>temporizadores, outbox, programador"]
    ST --> DB["db/<br/>migraciones y sqlc"]
    LAW["laws/<br/>pruebas de las cinco leyes"] -.-> ST
```

### Qué corre en producción

```mermaid
flowchart LR
    CONS["Consola (CDN)"] --> API["services/api<br/>kernel + platform + business"]
    WEB["Web (CDN)"] --> API
    MOB["Móvil"] --> API
    API --> PG[("PostgreSQL")]
    API --> RD[("Redis")]
    API -->|"señales"| N8N["n8n"]
    N8N -->|"API"| API
    AI["services/ai"] -->|"MCP"| API
```

## Reglas e invariantes

1. Cada carpeta compila, se prueba y se despliega sola; el CI solo corre lo que cambió.
2. Nombres de archivos y carpetas de código en inglés; `docs/` en español.
3. Nada generado se edita a mano (`packages/sdk`, `kernel/gen`).

## Decisiones relacionadas

- [ADR-0003 · Monorepo](../05-decisiones/0003-monorepo.md)
- [ADR-0004 · Contrato único](../05-decisiones/0004-contrato-unico.md)
