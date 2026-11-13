# Estándar de documentación

## Propósito

Definir cómo se escribe la documentación para que sea técnica, precisa y fácil de consultar.

## Alcance

Todo lo que está en `docs/`, incluidos los ADR.

## Reglas e invariantes

1. Una página resuelve una pregunta o explica un concepto.
2. Empieza explicando el propósito y el alcance.
3. Usa los términos del [glosario](../00-identidad/glosario.md) de forma consistente.
4. Prefiere párrafos cortos, tablas y listas.
5. Incluye diagramas Mermaid cuando ayudan a entender relaciones o flujos. Todos deben pasar `task docs:check`.
6. Incluye ejemplos reales cuando la explicación abstracta no basta.
7. Separa lo aceptado de lo propuesto y de lo pendiente.
8. No parece una conversación ni un ensayo.
9. Se actualiza en el mismo pull request que el código que describe.

## Funcionamiento

### Estructura de una página técnica

```markdown
# Nombre del tema

## Propósito
Qué resuelve esta parte.

## Alcance
Qué incluye y qué no incluye.

## Conceptos principales
Definiciones necesarias.

## Funcionamiento
Cómo funciona y cómo se relaciona con otros componentes.

## Reglas e invariantes
Qué condiciones siempre deben cumplirse.

## Ejemplo
Un caso concreto.

## Decisiones relacionadas
Enlaces a los ADR.

## Referencias
Documentos y contratos relacionados.
```

No toda página necesita todas las secciones. Un ADR, por ejemplo, usa su propia [plantilla](../05-decisiones/plantilla.md).

### ADR

- Una decisión importante se escribe **antes** del código que la implementa.
- Se numeran en orden (`0016-nombre-corto.md`) y nunca se reutiliza un número.
- Una decisión aceptada no se edita: se reemplaza con una nueva que la marca como `Reemplazada por ADR-NNNN`.
- Estados posibles: `Propuesta`, `Aceptada`, `Reemplazada por ADR-NNNN`, `Obsoleta`.

### Archivos y carpetas

- Carpetas numeradas por orden de lectura (`00-identidad`, `01-modelo`…).
- Cada carpeta tiene un `README.md` con el índice de sus páginas.
- Nombres de archivo en minúsculas, con guiones.

## Referencias

- [Google Developer Documentation Style Guide](https://developers.google.com/style): estructura, claridad, terminología y consistencia.
- [Microsoft Writing Style Guide](https://learn.microsoft.com/en-us/style-guide): escritura técnica, instrucciones y ejemplos.
- [GitHub · Managing and standardizing pull requests](https://docs.github.com/en/pull-requests/reference/managing-and-standardizing-pull-requests): plantillas y revisión.
