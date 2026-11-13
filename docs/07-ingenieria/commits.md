# Commits

## Propósito

Definir cómo se escribe un commit para que el historial se pueda leer y generar cambios por versión.

## Alcance

Todos los commits del repositorio.

## Reglas e invariantes

1. **En inglés**, con el formato [Conventional Commits](https://www.conventionalcommits.org/): `type(scope): summary`.
2. **`type`:** `feat`, `fix`, `docs`, `refactor`, `test`, `perf`, `build`, `ci`, `chore`.
3. **`scope`:** la carpeta o capa: `kernel`, `platform`, `business`, `contracts`, `console`, `web`, `mobile`, `ui`, `sdk`, `i18n`, `n8n`, `infra`, `docs`.
4. **Resumen:** imperativo, minúscula inicial, sin punto final, máximo 72 caracteres.
5. **Cuerpo (opcional):** qué y por qué, no cómo; líneas de 72 caracteres.
6. **Cambio incompatible:** `!` después del scope y una línea `BREAKING CHANGE:` en el pie.
7. **Autoría:** el commit lleva la identidad del desarrollador responsable. No se agregan `Co-Authored-By`, firmas ni menciones de herramientas o asistentes.
8. **Referencias:** `Closes #42` en el pie cuando cierra un issue.

## Ejemplo

```text
feat(kernel): enforce ledger conservation per resource

Add a deferred constraint that rejects any exchange whose entries
do not sum to zero for each resource.

Closes #42
```

Otros ejemplos válidos:

- `fix(platform): stop suspending spaces during grace period`
- `docs(platform): describe audited support access`
- `refactor(kernel)!: rename timer status values`
