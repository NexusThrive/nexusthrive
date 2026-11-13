# Accesibilidad

## Propósito

Garantizar que cualquier persona pueda usar NexusThrive con teclado, lector de pantalla, zoom, táctil o movimiento reducido.

## Alcance

Toda interfaz de NexusThrive. La base es **WCAG 2.2 nivel AA**, como requisito de implementación y no como repaso final.

## Conceptos principales

- **Nombre accesible:** lo que un lector de pantalla anuncia de un control.
- **Foco visible:** el contorno que muestra dónde está el teclado.
- **Anuncio:** un mensaje que el lector lee sin que la persona mueva el foco (`role="status"` o `role="alert"`).

## Funcionamiento

### Estructura

- Un `<h1>` por página; los niveles no saltan.
- Botón para acciones, enlace para navegar. Nunca un `div` con `onClick`.
- HTML nativo primero; ARIA solo para completar lo que el HTML no dice.
- Regiones con nombre donde ayudan a orientarse.

### Teclado y foco

- Todo se alcanza y se opera con teclado, en orden lógico.
- Foco visible siempre: 2 px en `--ring`.
- Diálogos y paneles atrapan el foco, cierran con Escape y devuelven el foco al control que los abrió.
- Menús, pestañas y combobox siguen los patrones de WAI-ARIA APG.

### Formularios

- Cada control tiene etiqueta asociada.
- Obligatorio: «*» visible y `aria-required`. Opcional: «(opcional)» cuando es la excepción.
- `type`, `inputMode` y `autoComplete` correctos.
- Error: texto e icono, `aria-invalid` y `aria-describedby`. Tras un envío fallido, aviso con `role="alert"` y foco al primer campo inválido. Lo escrito no se borra.
- Lo imprescindible va en la descripción visible del campo, nunca solo en un tooltip ni en el `placeholder`. El atributo `title` no cuenta como ayuda.

### Color, contraste y tamaño

- Texto ≥ 4,5:1 (≥ 3:1 si es grande); bordes de controles, foco e iconos con significado ≥ 3:1.
- Texto dorado con `--accent-text`, no con `--accent`.
- Los estados se distinguen sin color: icono, texto o forma.
- Objetivos táctiles de al menos 24 × 24 px; controles principales de `--control-h` (44 px con puntero táctil).
- Funciona con zoom al 200 % y sin scroll horizontal a 320 px.

### Anuncios

- Notificaciones y cargas: `role="status"`. Errores de envío: `role="alert"`.
- Iconos decorativos con `aria-hidden="true"`.
- Un evento se anuncia una sola vez.

## Reglas e invariantes

1. No se declara «cumple WCAG» sin una evaluación con alcance definido.
2. Toda historia de Storybook pasa el panel de accesibilidad (axe) en tema claro y oscuro.
3. Las pruebas de extremo a extremo ejecutan axe sobre las pantallas principales.
4. Cada pantalla cambiada pasa la revisión manual de abajo y el resultado se anota en el PR.
5. No se escriben puntajes que no se midieron.

## Ejemplo

Revisión manual de una pantalla cambiada:

1. Recorrerla solo con teclado: ¿llego a todo, veo el foco, Escape cierra?
2. Abrir y cerrar cada diálogo: ¿vuelve el foco al botón?
3. Zoom 200 % y ancho 320 px: ¿se lee todo sin scroll horizontal?
4. Con un lector (NVDA, VoiceOver o TalkBack): ¿se entienden los botones de icono, los errores y el resultado de guardar?
5. Con «Reducir movimiento»: ¿se entiende lo que cambió?

## Decisiones relacionadas

- [ADR-0016 · Sistema de diseño y licencia de fuentes](../../05-decisiones/0016-sistema-de-diseno.md)

## Referencias

- [WCAG 2.2](https://www.w3.org/TR/WCAG22/)
- [WAI-ARIA Authoring Practices](https://www.w3.org/WAI/ARIA/apg/)
- [testing.md](../../07-ingenieria/testing.md)
