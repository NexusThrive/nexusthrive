# Versiones

## Propósito

Definir cómo se versiona y se publica NexusThrive.

## Alcance

El binario `services/api`, las apps y el contrato.

## Reglas e invariantes

1. **Versionado semántico** (`MAJOR.MINOR.PATCH`) para el producto; las etiquetas son `vX.Y.Z`.
2. **`CHANGELOG.md`** en inglés, con el formato Keep a Changelog; cada versión agrupa Added, Changed, Fixed y Removed.
3. **Contrato:** los paquetes Protobuf llevan versión en el nombre (`kernel.v1`); un cambio incompatible crea `v2`.
4. **Eventos:** versión en el nombre (`.v1`); la versión anterior se mantiene durante la transición.
5. **Apps móviles:** se publican con la misma versión del producto y actualizaciones por Expo cuando no cambia código nativo.
6. **Pieles:** se versionan aparte, desde la consola ([ADR-0015](../05-decisiones/0015-pieles-versionadas.md)).

## Funcionamiento

1. Se actualiza `CHANGELOG.md` en un pull request `chore(release): vX.Y.Z`.
2. Al hacer merge se crea la etiqueta `vX.Y.Z`.
3. El CI construye y publica los artefactos y despliega a `production` con aprobación.
