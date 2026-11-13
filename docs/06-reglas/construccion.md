# Reglas de construcción

## Propósito

Estas reglas existen para que NexusThrive nunca se desordene. Se aplican a todo el equipo, personas y agentes de IA.

## Reglas e invariantes

### Sobre qué construir

1. **Primero la base.** Kernel, plataforma y consola se terminan antes de construir Business.
2. **Uno a la vez.** Solo un patrón en construcción. El siguiente empieza cuando el anterior tiene negocios pagando.
3. **Lo que no está terminado no existe.** Nada a medias visible para nadie.
4. **"Terminado" tiene definición:** la línea principal funciona de punta a punta y un negocio real la usa a diario.
5. **Antes de construir, se escribe.** Toda pieza nueva empieza con su documento en `docs/` y, si es importante, una decisión en `05-decisiones/`.

### Sobre cómo construir

6. **Una sola forma de hacer cada cosa.** Toda venta termina en la misma operación, entre por donde entre.
7. **Cada capacidad tiene un solo dueño** y sus dependencias se declaran en un solo lugar.
8. **La base cambia poco y con razón.** Si un patrón pide cambiar la base, primero se pregunta: ¿esto le sirve a todos? Si no, va en el patrón.
9. **El kernel no aprende palabras de negocio.** Si una necesidad parece obligarlo, se revisa el traductor.
10. **Lógica y vista nunca se mezclan.** Las caras solo muestran y piden.
11. **Nada toca la base de datos sino el kernel.**

### Sobre cómo trabajamos

12. **Un cambio de contrato** se hace junto con el kernel y las caras afectadas, en el mismo cambio.
13. **Toda operación nace con pruebas,** y las cinco leyes tienen pruebas que nunca se apagan.
