# Integración y despliegue continuos

## Propósito

Definir qué verifica la integración continua y cómo llega un cambio a producción.

## Alcance

GitHub Actions (`.github/workflows/`) y los entornos `staging` y `production`.

## Funcionamiento

```mermaid
flowchart LR
    PR["Pull request"] --> CH["Detectar qué cambió"]
    CH --> GO["Go: vet, staticcheck, test"]
    CH --> TS["TypeScript: lint, typecheck, test"]
    CH --> CT["Contratos: buf lint, buf breaking"]
    CH --> DC["Docs: render de Mermaid"]
    GO --> OK{"¿Todo en verde?"}
    TS --> OK
    CT --> OK
    DC --> OK
    OK -->|"sí"| M["Merge a main"]
    M --> STG["Despliegue automático a staging"]
    STG --> REL["Versión etiquetada"]
    REL --> PRD["Despliegue a production con aprobación"]
```

## Reglas e invariantes

1. El CI solo corre los trabajos de lo que cambió.
2. Ningún merge con el CI en rojo.
3. `main` se despliega solo a `staging`; `production` exige una versión etiquetada y aprobación.
4. Las migraciones de base de datos se aplican antes de desplegar el binario y deben ser compatibles con la versión anterior.
5. Todo despliegue se puede revertir a la versión anterior.
