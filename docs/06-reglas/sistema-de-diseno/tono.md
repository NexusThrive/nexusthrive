# Tono

## Propósito

Definir cómo escribe NexusThrive cuando habla con un negocio, con su equipo o con alguien que lo está conociendo.

## Alcance

Textos de producto, ayuda, sitio, correos, avisos, propuestas y redes, en todos los idiomas de `@nexusthrive/i18n`.

## Conceptos principales

1. **Claro antes que ingenioso.** Si una frase necesita explicación, se cambia.
2. **Concreto antes que grandioso.** «Cobra en 30 segundos» vence a «optimiza tu flujo de ingresos».
3. **Útil antes que persuasivo.** Primero qué puede hacer la persona; después por qué importa.
4. **Honesto antes que completo.** Si falta un dato, se dice; no se rellena.
5. **Breve.** Una idea por frase, una acción por pantalla.

## Funcionamiento

### Registro

- En español, **tú**. Formal solo en documentos legales.
- Monedas, fechas y números según el idioma y la región del negocio (`$1.250.000`, `25 de octubre de 2026`).
- Sin anglicismos innecesarios en la interfaz: «cobro», no «checkout»; «inventario», no «stock».
- Sin diminutivos ni condescendencia.
- El idioma del negocio, no el del kernel: «socio», «plan», «entrada». Nunca «participante», «compromiso» ni «módulo».

### Verbos de interfaz

| En vez de | Usa |
|---|---|
| Enviar / Aceptar | Cobrar, Guardar cliente, Registrar entrada, Anular venta |
| OK | Entendido, Continuar, Confirmar cobro |
| Error 422 | «No pudimos guardar el cliente: el correo ya existe» |
| Procesando… | «Guardando la venta…» |

### Estructura de un mensaje

1. **Qué pasó.**
2. **Qué significa** para el negocio.
3. **Qué hacer ahora**, en una acción.

### Tono por contexto

| Contexto | Tono | Ejemplo |
|---|---|---|
| Éxito | Breve y confirmatorio | «Venta registrada. Recibo enviado.» |
| Error | Directo, sin culpar | «No pudimos conectar con WhatsApp. Reintenta en un momento.» |
| Advertencia | Riesgo y salida | «Anular la venta deja rastro en el historial. ¿Continuar?» |
| Onboarding | Un paso a la vez | «Primero registra tu negocio. Después podrás cobrar.» |
| Propuesta de la IA | Sugiere, no decide | «Te sugiero recordarle a Ana su pago. ¿Lo envío?» |
| Incidente | Hechos, impacto y próxima actualización | «Revisamos un fallo en cobros con tarjeta. Actualizamos a las 3:00 p. m.» |

## Reglas e invariantes

1. Toda cifra, cliente, testimonio o comparación pública lleva fuente, fecha y autorización.
2. Se distingue siempre **disponible**, **en prueba** y **plan futuro**. Nunca se presenta un plan como disponible.
3. Palabras que no usamos: revolucionario, disruptivo, líder del mercado, de clase mundial, sinergia, solución integral, crecimiento exponencial, garantizado, última oportunidad, solo hoy.
4. Sin exclamaciones múltiples ni mayúsculas para gritar.
5. Enlaces con texto descriptivo («Ver el recibo»), nunca «clic aquí».
6. Ningún texto visible vive en el código: todo sale de `@nexusthrive/i18n`.

## Ejemplo

Error de cobro:

> No se pudo registrar el cobro: la caja está cerrada.
> Abre la caja y vuelve a intentarlo. La venta no se guardó.

## Decisiones relacionadas

- [ADR-0012 · Política de idiomas](../../05-decisiones/0012-politica-de-idiomas.md)
- [ADR-0013 · Internacionalización](../../05-decisiones/0013-internacionalizacion.md)

## Referencias

- [estados.md](estados.md)
- [`packages/i18n`](../../../packages/i18n/README.md)
