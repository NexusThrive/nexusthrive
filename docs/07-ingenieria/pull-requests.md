# Pull requests

## Propósito

Definir cómo se abre y se revisa un pull request para que cualquier desarrollador entienda el cambio.

## Alcance

Todos los pull requests. La plantilla está en `.github/pull_request_template.md`.

## Reglas e invariantes

1. **En inglés:** título, descripción y comentarios de revisión.
2. **Título** con el formato de [commits](commits.md): `feat(kernel): implement commitment validation`.
3. **Secciones obligatorias:** Summary, Motivation, Changes, Testing, Architecture impact.
4. **Pequeño y enfocado:** una historia por pull request. Si pasa de unas 400 líneas de código no generado, se divide.
5. **Documentación en el mismo pull request:** si cambia un comportamiento, cambia `docs/` en español.
6. **CI en verde** antes de pedir revisión.
7. **Sin firmas de herramientas** ni menciones de asistentes en la descripción.

## Ejemplo

```markdown
## Summary
Implement commitment validation in the kernel.

## Motivation
Prevent invalid or expired commitments from being fulfilled.

## Changes
- Add commitment validation.
- Enforce expiration rules.
- Add unit tests.

## Testing
- Unit tests
- Kernel law tests
- Static analysis

## Architecture impact
No changes to public contracts.

Closes #42
```
