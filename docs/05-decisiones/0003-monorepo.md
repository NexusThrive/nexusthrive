# 0003 · Un solo repositorio

**Estado:** aceptada

## Contexto
Casi todo cambio toca el contrato, el kernel y una cara a la vez. En varios repositorios esos cambios se desincronizan.

## Decisión
Un monorepo `nexusthrive` con carpetas independientes: `contracts/`, `kernel/`, `distributions/`, `experience/`, `integrations/`, `docs/`.

## Reglas
- Cada carpeta compila, se prueba y se despliega sola.
- Nadie importa código de otra carpeta: todo pasa por `contracts/`.
- El CI solo corre lo que cambió.

## Consecuencias
Cuando el kernel se abra a terceros, `kernel/` puede salir a su propio repositorio sin reescribir nada.
