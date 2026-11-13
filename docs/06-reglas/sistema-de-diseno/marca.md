# Marca

## Propósito

Fijar la identidad visual de NexusThrive para que cada pieza, pantalla o documento se reconozca como parte de lo mismo.

## Alcance

Logo, color, tipografía, forma, motivo gráfico e iconografía. Aplica a producto, sitio, presentaciones, propuestas y redes. El significado de la marca está en [00-identidad](../../00-identidad/README.md).

## Conceptos principales

- **Lockup:** la N con THRIVE debajo. Uso principal.
- **Símbolo:** la N sola. Navegación contraída, favicon, avatar e indicador de carga.
- **Wordmark:** Montserrat 700, mayúsculas y tracking amplio. Es el único lugar con mayúsculas sostenidas.
- **Motivo:** el corte diagonal de la N, a −24° (`--nx-angle`).

## Funcionamiento

### Logo

| Archivo | Uso |
|---|---|
| `logo/nexusthrive-lockup-ink.svg` | Lockup sobre papel o blanco |
| `logo/nexusthrive-lockup-white.svg` | Lockup sobre tinta o grafito |
| `logo/nexusthrive-mark-ink.svg` · `-white.svg` | Símbolo |
| `src/brand-logo.tsx` | Componente React; se tiñe con `currentColor` |

- Área de resguardo: al menos el ancho de la N alrededor del logo.
- Tamaño mínimo: el que mantiene legible «THRIVE». En impresos se comprueba a tamaño real.
- Fondos: tinta sobre papel o blanco; blanco sobre tinta.

### Color

El color sale del logo: bloque negro, esquinas rectas, acento oro.

| Rol | Token | HEX | Uso |
|---|---|---|---|
| Tinta | `--nx-ink` | `#000000` | Logo, texto fuerte |
| Tinta suave | `--nx-ink-soft` | `#1C1C20` | Texto de la interfaz |
| Papel | `--nx-paper` | `#F7F6F3` | Fondo claro |
| Blanco | `--nx-white` | `#FFFFFF` | Superficies |
| Grafito 950 | `--nx-graphite-950` | `#0A0A0B` | Fondo oscuro |
| Grafito 550 | `--nx-graphite-550` | `#5F5F67` | Texto secundario en claro |
| Grafito 200 | `--nx-graphite-200` | `#E8E6E1` | Bordes claros |
| Oro 500 | `--nx-gold-500` | `#D4AF37` | Acción principal, foco, dato que importa |
| Oro 700 | `--nx-gold-700` | `#7F6210` | Texto dorado legible sobre papel |
| Éxito | `--nx-success` | `#2E7D5B` | Solo estados |
| Peligro | `--nx-danger` | `#B3261E` | Solo estados |

Proporción: 60 % tinta y neutros, 30 % papel y blanco, 10 % oro. El oro marca **una** cosa por pantalla.

### Tipografía

| Familia | Uso | Pesos | Token |
|---|---|---|---|
| Montserrat | Títulos, cifras grandes, wordmark | 500 · 600 · 700 | `--font-display` |
| Inter | Interfaz, formularios, tablas | variable | `--font-sans` |
| JetBrains Mono | Códigos, referencias, identificadores | 400 · 500 | `--font-mono` |
| Public Stunt | Portadas y piezas de marca | 400 · itálica | `--font-stunt` |

Montserrat, Inter y JetBrains Mono son libres (SIL OFL 1.1). Public Stunt tiene licencia comercial registrada en `packages/brand/fonts/public-stunt/LICENSE.md`.

### Forma, espacio e iconos

- Radios suaves en tres niveles: controles 10 px, contenedores 12–24 px, estado en píldora. Diálogos y hojas 28 px.
- El borde separa; la sombra solo eleva lo que flota.
- Espacio con base de 4 px.
- Iconos lineales, de trazo consistente y sin relleno decorativo. En acciones destructivas el icono acompaña al texto, no lo reemplaza.

## Reglas e invariantes

1. El logo no se deforma, rota, sombrea, encierra en formas ni se recolorea fuera de tinta y blanco.
2. No hay versiones en oro, degradados ni contornos del logo.
3. No se crean paletas nuevas. El oro nunca es fondo de bloques grandes.
4. Los colores de estado nunca decoran.
5. Public Stunt nunca se usa en texto de interfaz, y solo dentro de lo que cubre su licencia.
6. El motivo diagonal aparece una vez por pantalla: cargas, progreso o destellos. Nunca como decoración repetida.

## Ejemplo

Una portada de presentación: fondo grafito 950, lockup en blanco, título en Public Stunt (pieza exportada), subtítulo en Montserrat 500 y una sola línea en oro 500 con el dato principal.

## Decisiones relacionadas

- [ADR-0016 · Sistema de diseño y licencia de fuentes](../../05-decisiones/0016-sistema-de-diseno.md)

## Referencias

- [tokens.md](tokens.md)
- [`packages/brand`](../../../packages/brand/README.md)
