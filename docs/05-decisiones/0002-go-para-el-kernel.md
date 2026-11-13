# 0002 · Go para el kernel, TypeScript para las caras, Python para análisis

**Estado:** aceptada

## Contexto
El kernel orquesta eventos, mueve valor y atiende a muchos participantes a la vez. Debe ser un lenguaje, no un framework.

## Decisión
- **Go** para el kernel y las extensiones: binario único, concurrencia nativa, simple de leer.
- **TypeScript + React** para las caras: web, móvil (Expo) y escritorio (Tauri).
- **Python** para análisis e IA, como servicio aparte.

## Alternativas descartadas
- **Rust:** más rápido, pero la curva de aprendizaje frena a un equipo pequeño.
- **TypeScript en el kernel:** reutiliza tipos, pero no da la solidez buscada para un núcleo.
- **Java:** duplica el papel de Go.
