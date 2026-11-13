# ADR-0002 · Go en el kernel, TypeScript en las caras y Python para análisis

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

El kernel orquesta eventos, mueve valor y atiende a muchos participantes a la vez. Debe ser un lenguaje, no un framework. Las caras necesitan un ecosistema maduro para web, móvil y escritorio. El análisis y la IA tienen su mejor ecosistema en Python.

## Decisión

Go 1.24 para el kernel, la plataforma, las distribuciones y `services/api`. TypeScript con React para las apps y los paquetes compartidos. Python para `services/ai`.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Rust en el kernel | Más rápido y seguro, pero su curva de aprendizaje frena a un equipo pequeño. |
| TypeScript en todo | Comparte tipos, pero no da la solidez buscada para el núcleo. |
| Java en el análisis | Duplica el papel de Go y no tiene el ecosistema de IA de Python. |

## Consecuencias

### Positivas

- Un binario pequeño, rápido y concurrente.
- Una sola base de componentes para todas las caras.

### Negativas

- Tres lenguajes que mantener.
- La lógica existente en TypeScript se reescribe en Go.

### Riesgos

- Que la lógica de negocio se filtre a las apps de TypeScript.

## Cumplimiento

El CI tiene un trabajo por lenguaje. Las apps solo dependen de `@nexusthrive/sdk` para hablar con el backend.

## Referencias

- [Stack](../02-arquitectura/stack.md)
