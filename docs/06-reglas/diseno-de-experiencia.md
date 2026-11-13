# Diseño de experiencia

## Propósito

Definir lo que cumple toda pantalla de NexusThrive: apps de negocios y consola.

## Reglas e invariantes


1. **Una acción principal por pantalla.** En Cobrar, el botón es cobrar. Lo demás espera.
2. **El idioma del negocio.** Socio, plan, entrada. Nunca "participante", "compromiso" ni "módulo".
3. **Nada que configurar.** El dueño no ve interruptores de funciones. Si necesita algo, lo solicita.
4. **Toda pantalla tiene cuatro estados:** cargando, vacía (con qué hacer), con datos y con error (con cómo salir).
5. **Dinero siempre claro.** Cada monto dice si está pagado, pendiente o vencido, con color y con texto.
6. **Rápido en el mostrador.** Check-in y cobro en menos de tres toques desde el móvil.
7. **Funciona en cualquier pantalla.** Diseñar a 375 px y a 1280 px; teclado y foco visibles.
8. **La IA propone, la persona decide.** Mensajes de cobro, ajustes y resúmenes llegan como sugerencias con un botón para aprobar.
9. **Componentes, no pantallas sueltas.** Todo se arma con `@nexusthrive/ui` y se documenta primero en Storybook.
10. **Textos internacionalizados.** Ningún texto visible vive en el código; todo sale de `@nexusthrive/i18n` en español e inglés.
