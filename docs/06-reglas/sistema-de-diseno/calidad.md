# Calidad de interfaz

## Propósito

Dar una lista única de revisión para todo cambio que crea o modifica una pantalla, un formulario, un componente o una interacción.

## Alcance

Todo PR con interfaz, en cualquier app o en `@nexusthrive/ui`. Se copia en la descripción del PR y se marca lo que aplica. Lo que no se pudo verificar se dice.

## Conceptos principales

- **Antes de construir:** entender la tarea y elegir el patrón.
- **Al construir:** usar tokens y componentes compartidos.
- **Antes del PR:** probar, revisar y documentar.

## Funcionamiento

### Antes de construir

- [ ] Sé qué tarea resuelve la pantalla y para qué rol.
- [ ] Busqué si ya existe un componente o una interacción igual en Storybook.
- [ ] Elegí el patrón con [interaccion.md](interaccion.md).
- [ ] Tengo los estados: cargando, vacía, sin coincidencias, error, éxito y sin permiso ([estados.md](estados.md)).

### Al construir

- [ ] Uso tokens y componentes compartidos: nada de colores, sombras, `z-index` o duraciones sueltos; nada de modales hechos a mano ni `window.confirm`.
- [ ] Formularios con etiqueta, descripción y error en cada campo.
- [ ] Teclado, foco y táctil funcionan; botones de icono con `aria-label`.
- [ ] Movimiento con tokens, y nada se rompe con movimiento reducido.
- [ ] Ningún texto visible en el código: todo sale de `@nexusthrive/i18n`, en español e inglés.
- [ ] Textos concretos según [tono.md](tono.md) («Guardar cliente», no «Enviar»).

### Antes del PR

- [ ] Lint, tipos y pruebas en verde.
- [ ] Historia de Storybook con todos los estados, si es un componente compartido.
- [ ] Panel de accesibilidad de Storybook sin errores, en claro y oscuro.
- [ ] Probado a 320, 360, 412, 820, 1180 y 1280 px, sin errores en consola.
- [ ] Revisión manual de [accesibilidad.md](accesibilidad.md).
- [ ] Capturas antes y después en el PR, sin datos personales.
- [ ] Si cambió el sistema (token, componente, patrón), actualicé esta carpeta en el mismo PR.

## Reglas e invariantes

1. Un PR con interfaz no se fusiona sin esta lista.
2. Lo que no se verificó se declara en el PR; no se marca como hecho.
3. Las reglas automáticas (sin `z-index` numérico, sin modales a mano, sin textos en el código) viven en pruebas de arquitectura y en el lint, y solo se endurecen.

## Ejemplo

Un PR agrega el formulario «Nuevo plan». La descripción incluye la lista, con «Probado a 320 px» marcado, capturas en claro y oscuro y una nota: «Sin lector de pantalla en Android: pendiente».

## Decisiones relacionadas

- [ADR-0016 · Sistema de diseño y licencia de fuentes](../../05-decisiones/0016-sistema-de-diseno.md)

## Referencias

- [pull-requests.md](../../07-ingenieria/pull-requests.md)
- [testing.md](../../07-ingenieria/testing.md)
