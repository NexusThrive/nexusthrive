# Movimiento

## Propósito

Definir cuándo y cómo se anima algo para que el movimiento explique en vez de adornar.

## Alcance

Transiciones de vistas, diálogos, paneles, menús, cifras, arrastre y entradas de bloques, en todas las apps.

## Conceptos principales

- **Todo nace de la N:** el trazo recorre el contorno y los cortes caen a −24°.
- **Dueño de una transición:** la única técnica que anima una propiedad de un elemento.
- **Movimiento reducido:** una variante de primera clase, no un parche.

## Funcionamiento

### Tokens

| Token | Duración | Para |
|---|---|---|
| `--d1` · `--motion-feedback` | 120 ms | Presionar, marcar |
| `--d2` · `--motion-hover` | 200 ms | Hover, color, cambios en su sitio |
| `--motion-popover` | 160 ms | Tooltip, menú, ayuda |
| `--d3` | 320 ms | Barra lateral, pestañas |
| `--motion-overlay-in` | 360 ms | Diálogo y panel entran |
| `--motion-overlay-out` | 200 ms | Diálogo y panel salen |
| `--motion-view` | 360 ms | Entrada de una vista |
| `--d4` | 600 ms | Entradas de bloques y cifras |

Curvas: `--nx-ease-out` (entradas), `--nx-ease-in` (salidas), `--nx-ease-in-out` (barridos), `--nx-ease-spring` (indicadores que se deslizan), `--nx-ease-bounce` (interruptores).

### Quién anima qué

| Superficie | Dueño |
|---|---|
| Entrada de una vista | CSS |
| Diálogo y panel lateral | CSS; en escritorio el diálogo puede nacer del botón que lo abrió |
| Hoja inferior móvil | CSS; se arrastra para cerrar |
| Menús, tooltips, ayuda | CSS |
| Cifras, barras y trazos | Web Animations API |
| Arrastrar y soltar | Librería de arrastre, solo con `transform` |

### Patrones

- **Diálogo que nace del botón (escritorio):** arranca con el tamaño y la posición del control, crece hasta su sitio y al cerrar vuelve a él. Si el control ya no existe o hay movimiento reducido, entra centrado.
- **Panel lateral:** entra y sale por la derecha, en espejo.
- **Listas:** escalonado solo en la primera carga, 50 ms por ítem y máximo ~8 ítems.
- **Cambio de sección en móvil:** la nueva entra desde la derecha sobre la anterior; al volver, la actual sale hacia la derecha.

## Reglas e invariantes

1. Si no explica de dónde viene algo, qué cambió o que se guardó, no se anima.
2. Nada retrasa una acción: el control responde al instante y el foco no espera a la animación.
3. Solo se animan `transform`, `opacity`, `filter` y `clip-path`. Nunca `width`, `height`, `top`, `left` ni sombras en bucle, salvo una excepción documentada en el PR.
4. Un dueño por transición: nunca CSS y JavaScript sobre la misma propiedad del mismo elemento.
5. Sin librerías de animación mientras CSS y Web Animations cubran el caso.
6. Con `prefers-reduced-motion` o el ajuste «Reducir movimiento» (`data-motion="off"` en `<html>`), toda animación dura 1 ms y el cambio se entiende por texto, color o icono.
7. Nada parpadea más de tres veces por segundo.

## Ejemplo

Guardar un cliente desde un diálogo: el botón muestra «Guardando…» (120 ms), el diálogo sale en 200 ms con `--nx-ease-in`, la fila nueva aparece en la lista con un fundido de 200 ms y una notificación confirma. Con movimiento reducido, todo ocurre al instante y la notificación sigue diciendo qué pasó.

## Decisiones relacionadas

- [ADR-0016 · Sistema de diseño y licencia de fuentes](../../05-decisiones/0016-sistema-de-diseno.md)

## Referencias

- [tokens.md](tokens.md)
- [accesibilidad.md](accesibilidad.md)
