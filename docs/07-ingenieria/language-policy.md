# Política de idiomas

## Propósito

Definir qué idioma se usa en cada parte de NexusThrive para no mezclar español e inglés sin criterio.

## Alcance

Todo el repositorio, los issues, los pull requests y la interfaz del producto.

## Conceptos principales

- **Español:** documentación del proyecto.
- **Inglés:** código fuente, comentarios técnicos, commits y pull requests.
- **Español e inglés:** interfaz del producto, mediante internacionalización, sin mezclar textos de usuario con la lógica.

Esta división es una decisión propia de NexusThrive. Las guías de estilo de Google y Microsoft se usan para escribir con claridad, no para imponer esta combinación.

## Reglas e invariantes

| Elemento | Idioma obligatorio |
|---|---|
| Documentación en `docs/` | Español |
| `README.md` principal | Español |
| `AGENTS.md`, `CONTRIBUTING.md`, `CHANGELOG.md` | Inglés |
| Código fuente, variables y funciones | Inglés |
| Clases, tipos e interfaces | Inglés |
| Nombres de archivos y carpetas de código | Inglés |
| Comentarios del código | Inglés |
| Mensajes de error internos y logs | Inglés |
| Pruebas y nombres de casos de prueba | Inglés |
| Contratos, esquemas, tablas y columnas | Inglés |
| Commits | Inglés |
| Pull requests e issues técnicos | Inglés |
| Nombres de ramas | Inglés |
| Plantillas de `.github/` | Inglés |
| Textos visibles al usuario | Internacionalizados (`es` por defecto, `en`) |

**Excepción:** los nombres oficiales de producto conservan su identidad y no se traducen: NexusThrive, Thrive Business y los nombres comerciales de sus distribuciones.

## Ejemplo

En la documentación, la explicación va en español y los nombres técnicos se mantienen en inglés:

> El componente de ejecución (`Execution Engine`) procesa operaciones autorizadas. Cada operación se ejecuta dentro de una transacción (`Transaction`) y deja un evento (`Event`).

En el código, todo en inglés:

```go
// AssignMembership creates a membership for the given participant.
// It validates the plan and records the resulting commitment.
func AssignMembership(participantID string, planID string) error {
	return nil
}
```

No permitido:

```go
func asignarMembresia(participanteID string, planID string) error {
	return nil
}
```

Una tabla se llama `memberships`, nunca `membresias`. Un texto de pantalla se referencia por su clave (`business.members.title`), nunca escrito en el componente.

## Decisiones relacionadas

- [ADR-0012 · Política de idiomas](../05-decisiones/0012-politica-de-idiomas.md)
- [ADR-0013 · Internacionalización de la interfaz](../05-decisiones/0013-internacionalizacion.md)
