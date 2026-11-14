# ADR-0023 · URLs y dominios

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

NexusThrive necesita un esquema de URLs para el sitio, la web de los negocios, la consola, la API y la documentación, en `staging` y `production`. Cada negocio es un espacio del kernel y la web debe llevar a cada persona al suyo. El dominio registrado es `nexusthrive.com.co`; los docs decían `nexusthrive.com`.

## Decisión

Todo cuelga de `nexusthrive.com.co`, con nombres de un solo nivel de subdominio para que un certificado comodín los cubra.

| URL | Qué es |
|---|---|
| `nexusthrive.com.co` | Sitio de venta |
| `app.nexusthrive.com.co` | Web de los negocios (`apps/web`) |
| `console.nexusthrive.com.co` | Consola del equipo (`apps/console`) |
| `api.nexusthrive.com.co` | API Connect-RPC y JSON/HTTP, `/mcp` y `/webhooks/wompi` |
| `docs.nexusthrive.com.co` | Documentación pública de API, MCP y webhooks |
| `n8n.nexusthrive.com.co` | n8n, solo para el equipo |
| `staging.`, `staging-console.`, `staging-api.`, `storybook.` | Los mismos servicios en staging |

**Un negocio se identifica por una ruta con su `slug`:**

```
app.nexusthrive.com.co/mi-tienda/inicio
app.nexusthrive.com.co/mi-tienda/ventas
app.nexusthrive.com.co/mi-tienda/ajustes/api
```

- El `slug` solo localiza: el acceso lo decide el kernel con la sesión y la pertenencia de la persona al espacio. Conocer la URL no da acceso.
- El `slug` es único, en minúsculas y con letras, números y guiones. Hay una lista de nombres reservados (`api`, `app`, `admin`, `console`, `docs`, `staging`, `n8n`, entre otros).
- Se fija al crear el negocio. Un cambio posterior deja una redirección desde el anterior.
- Las rutas de la interfaz van en español (`inicio`, `ventas`, `ajustes`), ya que son texto visible para la persona y salen de `@nexusthrive/i18n`; el `slug` del negocio no se traduce.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Subdominio por negocio (`mi-tienda.nexusthrive.com.co`) | Exige certificados y DNS comodín y complica la sesión entre subdominios; se puede añadir después. |
| Dominio propio de cada negocio | Fuera del MVP; requiere verificación de dominio y certificados por negocio. |
| Identificar el negocio solo por la sesión, sin slug en la URL | No se pueden compartir enlaces ni abrir dos negocios en pestañas distintas. |

## Consecuencias

### Positivas

- Un solo origen para la web: un solo DNS, un solo certificado y una sola sesión de Clerk.
- Enlaces compartibles y varias pestañas con distintos negocios.

### Negativas

- La URL no refleja la marca del negocio.

### Riesgos

- Que un `slug` sea adivinable y alguien lo interprete como control de acceso. Mitigación: el kernel autoriza siempre por espacio (ley de permiso) y hay pruebas de aislamiento.
- Colisión de un `slug` con una ruta futura de la aplicación. Mitigación: lista de nombres reservados y prueba que la valida.

## Cumplimiento

- Prueba: un `slug` reservado o repetido se rechaza al crear el negocio.
- Prueba de punta a punta: una persona sin pertenencia a `mi-tienda` recibe "sin permiso" aunque conozca la URL.

## Referencias

- [ADR-0006](0006-identidad.md)
- [ADR-0013](0013-internacionalizacion.md)
- [ADR-0019](0019-alojamiento-y-entornos.md)
- [Despliegue](../02-arquitectura/despliegue.md)
