# ADR-0016 · Sistema de diseño en un paquete de marca y licencia de fuentes registrada

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

Las apps (consola, web, móvil y escritorio) necesitan verse como un solo producto. Sin una fuente única de valores, cada app copia colores, radios y duraciones, y la marca se desvía con cada pantalla. Además, la identidad usa una fuente comercial, Public Stunt, que solo puede vivir en el repositorio con su licencia registrada.

## Decisión

La marca vive en `packages/brand`: tokens en CSS (`tokens.css`, fuente de la verdad) con su espejo `tokens.json`, el tema de Tailwind v4, los estilos base, el logo y las fuentes. Los componentes viven en `@nexusthrive/ui` y solo usan tokens. Las reglas de uso se documentan en `docs/06-reglas/sistema-de-diseno`.

La escala de radios suave de `tokens.css` es la canónica: `xs` 6, `sm` 8, `md` 10 (controles), `lg` 12 (tarjetas), `xl` 14, `2xl` 16 (menús, avisos), `3xl` 24 (widgets), `sheet` 28 (diálogos y hojas) y `full` (estado en píldora). Las esquinas rectas solo aplican al logo.

Public Stunt se incluye con su licencia comercial registrada en `packages/brand/fonts/public-stunt/LICENSE.md`, solo para piezas de marca y nunca para texto de interfaz. Montserrat, Inter y JetBrains Mono son libres (SIL OFL 1.1) y cada app las aloja.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Tokens dentro de `@nexusthrive/ui` | Las piezas de marca, el sitio y las herramientas de diseño necesitan los tokens sin depender de componentes React. |
| Preset de Tailwind en `@nexusthrive/config` | Separa los valores de su documentación y obliga a Tailwind a quien solo necesita CSS. |
| Tokens en JSON con generación de CSS | Agrega una herramienta antes de necesitarla; el CSS es legible y suficiente. |
| Radios casi nulos (2–8 px) | La interfaz necesita una jerarquía visible entre controles, contenedores y superficies flotantes; con radios casi nulos todo se ve igual de plano. |
| Dejar Public Stunt fuera del repositorio | Las piezas de marca dependerían de copias locales sin control. |

## Consecuencias

### Positivas

- Un solo lugar para cambiar un color o una duración en todas las apps.
- Tema claro y oscuro, densidad compacta y táctil desde el primer día.
- Licencia de fuentes visible y auditable.

### Negativas

- Dos archivos de tokens que mantener sincronizados (CSS y JSON).

### Riesgos

- Que el tipo de licencia de Public Stunt no cubra incrustarla en web o apps. Mitigación: confirmarlo y actualizar el registro antes de servirla.
- Que `tokens.css` y `tokens.json` se desincronicen. Mitigación: se revisa en cada PR y se automatiza una comprobación cuando exista la primera app.

## Cumplimiento

- La revisión de PR rechaza valores sueltos (colores, sombras, `z-index`, duraciones) fuera de `packages/brand`.
- Cuando exista `@nexusthrive/ui`, una prueba de arquitectura vigila estas reglas.
- Todo cambio de logo, paleta o tipografía requiere un ADR nuevo.

## Referencias

- [Sistema de diseño](../06-reglas/sistema-de-diseno/README.md)
- [`packages/brand`](../../packages/brand/README.md)
- [Registro de licencia de Public Stunt](../../packages/brand/fonts/public-stunt/LICENSE.md)
