# Calidad del código

## Propósito

Definir cómo se escribe y se comenta el código de NexusThrive.

## Alcance

Go, TypeScript, Python, SQL y configuración.

## Reglas e invariantes

### Generales

1. Todo en inglés: identificadores, comentarios, errores internos, logs y pruebas.
2. Nombres que explican: `CommitmentValidator`, no `CV` ni `helper2`.
3. Funciones pequeñas con una sola responsabilidad.
4. Sin código muerto ni comentado; para eso está git.
5. Sin textos visibles en el código: se usan claves de `@nexusthrive/i18n`.
6. Dinero con decimales exactos; nunca `float`.

### Comentarios

Los comentarios explican lo que el código no puede expresar por sí solo. Van inmediatamente encima del elemento que explican, son breves y se mantienen actualizados.

| Tipo | Qué debe explicar |
|---|---|
| Función | Propósito, comportamiento y condiciones importantes |
| Tipo o clase | Responsabilidad y restricciones |
| Algoritmo complejo | Por qué se usa ese enfoque |
| Regla de negocio | Por qué existe esa condición |
| Decisión técnica | Por qué se evita una alternativa |
| `TODO` | Qué falta, con el issue relacionado: `// TODO(#42): ...` |

Cinco reglas:

1. Explicar el propósito, no narrar cada línea.
2. Explicar por qué existe una condición especial.
3. Documentar restricciones, efectos secundarios y riesgos.
4. No repetir el nombre de la función con otras palabras.
5. Actualizar o borrar el comentario cuando cambia el comportamiento.

En Go, el comentario de todo elemento exportado empieza con su nombre.

### Por lenguaje

| Lenguaje | Herramientas obligatorias |
|---|---|
| Go | `gofmt`, `go vet`, `staticcheck`; errores con contexto (`fmt.Errorf("...: %w", err)`) |
| TypeScript | `strict: true`, ESLint, Prettier; sin `any` salvo en código generado |
| Python | `ruff`, `mypy` en modo estricto |
| SQL | Migraciones versionadas; consultas con `sqlc`; nunca SQL armado con cadenas |
| Protobuf | `buf lint` y `buf breaking` |

## Ejemplo

Bien documentado:

```go
// ValidateCommitment checks whether a commitment can be fulfilled.
// It rejects expired or previously completed commitments.
func ValidateCommitment(commitment Commitment) error {
	return nil
}
```

Mal documentado:

```go
// This function is called ValidateCommitment.
// It receives a commitment.
// It checks the commitment.
// It returns an error if something goes wrong.
func ValidateCommitment(commitment Commitment) error {
	return nil
}
```
