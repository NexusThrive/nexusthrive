# Tokens

## Propósito

Listar los valores con nombre del sistema y decir cuándo se usa cada uno.

## Alcance

Color, tipografía, espacio, forma, sombra, capas, tamaños y layout. El movimiento está en [movimiento.md](movimiento.md).

## Conceptos principales

- **Primitivo (`--nx-*`):** el valor crudo (un HEX, unos px). Solo lo usan los semánticos.
- **Semántico:** el rol (`--background`, `--accent`, `--border-input`). Es lo que usan los componentes.
- **Tema:** claro por defecto; oscuro con la clase `.dark` o `data-theme="dark"` en `<html>`.
- **Densidad:** normal o compacta (`data-density="compact"`).

## Funcionamiento

### Color semántico

| Token | Claro | Oscuro | Uso |
|---|---|---|---|
| `--background` | papel | grafito 950 | Fondo de la app |
| `--surface` | blanco | grafito 900 | Tarjetas, paneles, diálogos |
| `--surface-muted` | grafito 100 | grafito 800 | Campos, zonas secundarias |
| `--border` · `--border-strong` | grafito 200 · `#D9D7D1` | grafito 700 · `#3A3A40` | Separadores |
| `--border-input` | `#D2D0CA` | `#3A3A40` | Controles editables |
| `--foreground` · `--foreground-muted` | tinta suave · grafito 550 (5,9:1) | papel · grafito 350 (8,5:1) | Texto |
| `--primary` | tinta | oro 500 | Texto fuerte e identidad |
| `--accent` · `--accent-text` | oro 500 · oro 700 | oro 500 | Acción principal; texto dorado legible |
| `--ring` | `#8F6F12` | oro 500 | Foco visible |
| `--success` · `--danger` · `--warning-*` | verde · rojo · oro | más claros | Estados |
| `--overlay` | tinta 32 % | negro 50 % | Fondo de diálogos |
| `--doc-*` | fijos | fijos | Recibos y facturas: iguales en cualquier tema |

La acción principal va en oro (`--accent`, tinta encima, 9,6:1) por la variante del botón. `--primary` no se cambia a oro: muchos textos lo usan y perderían contraste.

### Tipografía

Escala (`--text-*`): 11 · 12 · 13 · 14 · 16 · 18 · 20 · 24 · 30 · 40 · 52 px.

- 11 px (`text-2xs`) solo para metadatos.
- 13 px (`text-ui`) para texto operativo: tablas, listas, menús.
- Títulos en Montserrat 500; la negrita queda para cifras y nombres propios.
- Texto corrido hasta `--content-prose` (72 caracteres).
- Cifras tabulares en tablas y montos.

### Espacio, forma y sombra

| Grupo | Tokens |
|---|---|
| Espacio (base 4 px) | `--space-1` … `--space-20` (4–80 px) |
| Radios | `xs` 6 · `sm` 8 · `md` 10 (controles) · `lg` 12 (tarjetas) · `xl` 14 · `2xl` 16 (menús, avisos) · `3xl` 24 (widgets) · `sheet` 28 (diálogos, hojas) · `full` para estado |
| Sombras | `xs` campos · `sm` tarjeta al pasar el cursor · `md` menús y ayuda · `lg` diálogos y paneles · `focus` halo oro de un campo |

### Capas

| Capa | Token | Valor |
|---|---|---|
| Elevado | `--z-raised` | 10 |
| Encabezado fijo | `--z-sticky` | 40 |
| Barra inferior móvil | `--z-tabbar` | 45 |
| Barra lateral | `--z-sidebar` | 50 |
| Barra superior | `--z-topbar` | 55 |
| Fondo de diálogo | `--z-overlay` | 70 |
| Diálogo, panel, hoja | `--z-modal` | 71 |
| Aviso sobre un panel | `--z-alert-overlay` · `--z-alert` | 72 · 73 |
| Menú, tooltip, ayuda | `--z-popover` | 80 |
| Aviso de conexión | `--z-banner` | 85 |
| Notificación | `--z-toast` | 90 |
| Pantalla completa | `--z-takeover` | 120 |

Un menú dentro de un diálogo siempre queda encima del diálogo.

### Tamaños y layout

| Grupo | Tokens |
|---|---|
| Controles | `--control-h` 40 px (34 compacta, 44 con puntero táctil) · `--row-h` 56 px |
| Diálogos | `--dialog-w-sm` 440 · `md` 520 · `lg` 640 · `xl` 768 |
| Paneles y flotantes | `--drawer-w` 440 · `--drawer-w-lg` 560 · `--popover-w` 320 |
| Layout | `--sidebar-width` 248 · contraída 76 · `--topbar-height` 60 (72 desde 768 px) · `--content-max` 1280 (1480 desde 1536 px) |
| Puntos de quiebre | `sm` 640 · `md` 768 · `lg` 1024 · `xl` 1280 |

### Foco

`:focus-visible` dibuja 2 px en `--ring`, separado 2 px del control.

## Reglas e invariantes

1. Un componente usa semánticos, nunca primitivos ni valores sueltos.
2. Sin `rounded-[Npx]`, `shadow-[…]`, `text-[Npx]` ni `z-index` numéricos.
3. El contorno de foco nunca se quita sin reemplazarlo por uno igual de visible.
4. Un token nuevo se agrega en `tokens.css`, en `tokens.json` y en esta página, en el mismo cambio.

## Ejemplo

```tsx
<div className="rounded-lg border border-border bg-surface p-6 shadow-sm">
  <p className="text-ui text-muted">Saldo pendiente</p>
  <p className="font-display text-2xl tabular-nums">$1.250.000</p>
</div>
```

## Decisiones relacionadas

- [ADR-0016 · Sistema de diseño y licencia de fuentes](../../05-decisiones/0016-sistema-de-diseno.md)

## Referencias

- [`packages/brand/css/tokens.css`](../../../packages/brand/css/tokens.css)
- [`packages/brand/tokens/tokens.json`](../../../packages/brand/tokens/tokens.json)
