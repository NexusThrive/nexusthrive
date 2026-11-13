# ADR-0013 · Interfaz internacionalizada

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

El producto se vende primero en Colombia en español, pero debe poder mostrarse en inglés sin reescribir pantallas.

## Decisión

Todo texto visible sale de `@nexusthrive/i18n` mediante claves estables (`area.pantalla.elemento`). El español (`es`) es el idioma por defecto y el inglés (`en`) se mantiene en paralelo. El vocabulario de cada piel también se traduce.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Textos en el código | Imposible traducir sin tocar la lógica. |

## Consecuencias

### Positivas

- Un mercado nuevo es un archivo de traducción.

### Negativas

- Cada texto nuevo exige su clave en dos idiomas.

### Riesgos

- Claves sin traducir que lleguen a producción.

## Cumplimiento

Una prueba falla si una clave existe en `es` y no en `en`, o al revés.

## Referencias

- [@nexusthrive/i18n](../../packages/i18n/README.md)
