# HU-gateway-11 · Tokens de Clerk y caché JWKS acotada

## Propósito y alcance

Implementar el ADR-0040 aceptado sobre el verificador existente. La resolución por slug sigue en HU-gateway-12 y la prueba con Clerk real en HU-gateway-22; esta historia usa emisores y servidores JWKS locales de prueba.

## Cambios

- Verificación RS256, emisor exacto, sujeto no vacío, expiración, inicio de vigencia y lista exacta de partes autorizadas. Una audiencia configurada debe aparecer en `aud`, como cadena o lista. Claims temporales nulos o mal formados se rechazan.
- `CLERK_AZP` acepta una lista separada por comas o espacios y es obligatoria fuera de desarrollo. `CLERK_AUDIENCE` es opcional. Las variables se documentan en `.env.example`.
- La fuente de llaves mantiene por separado el último intento y la última descarga válida: caché fresca de 5 minutos, uso máximo de una hora y separación de intentos de 30 segundos, incluso si nunca hubo una descarga válida.
- Las renovaciones concurrentes se comparten. Esperar una renovación respeta el contexto de cada petición; el mutex de la caché no se mantiene durante la petición HTTP.
- Un fallo de red o documento inválido conserva temporalmente las llaves anteriores, sin renovar su antigüedad. Una respuesta válida reemplaza el conjunto completo y retira las llaves que ya no aparecen.
- Solo se consulta la URL configurada. HTTPS es obligatorio salvo permiso explícito para pruebas o desarrollo; no se siguen redirecciones a otro origen. Se limita la descarga a 1 MiB y 5 segundos, se rechazan `kid` duplicados y se validan RSA, uso, algoritmo y exponente.
- Las respuestas de autenticación siguen siendo genéricas. Los registros internos reciben categorías fijas mediante `OnReject`, sin token, firma ni datos personales.

## Evidencia

Las pruebas se escribieron primero y fallaron por ausencia de los campos de configuración necesarios. Pasan los casos de partes autorizadas y audiencia, fechas inválidas, privacidad del diagnóstico, caída dentro y fuera del máximo, limitación de intentos fallidos iniciales, concurrencia, rotación, retirada de llave, descarga inválida sin rejuvenecer la caché, duplicados, tamaño excesivo y redirección a otro origen. El fixture de rotación asigna un `kid` distinto a la llave nueva, porque los emisores de desarrollo usan un identificador fijo.

Pasan `task lint` y `task test`, incluidas arquitectura, las cinco leyes y los contratos. Se utiliza Go 1.24.7 y PostgreSQL del proyecto en 55432. La comprobación con detector de carreras se ejecuta sobre las nuevas pruebas de tokens y JWKS.

## Referencias

- [ADR-0040](../../05-decisiones/0040-validacion-de-clerk-y-cache-jwks.md).
- [Backlog M2](../../product/backlog/m2-puertas-del-kernel.md).
