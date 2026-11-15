# ADR-0040 · Validación de Clerk y vigencia de la caché JWKS

- **Estado:** Propuesta
- **Fecha:** 2026-11-15
- **Responsables:** santy401

## Contexto

HU-gateway-04 debe fijar la validación de tokens antes de HU-gateway-10 y HU-gateway-11. El corte del ADR-0036 ya verifica RS256, emisor, expiración, inicio de vigencia y una parte autorizada opcional. Su caché renueva ante un `kid` desconocido, pero una llave conocida puede permanecer indefinidamente y los fallos antes de obtener la primera caché no quedan sujetos al mismo límite de renovación.

La autenticación debe seguir siendo verificable sin una cuenta real de Clerk. Las pruebas con la instancia real corresponden a HU-gateway-22; la resolución del participante y del espacio se rige por los ADR-0038 y 0039.

## Decisión propuesta

### Token y configuración

Se conserva RS256 como único algoritmo de M2. Se rechazan `none`, algoritmos simétricos, ausencia de `kid`, firma ajena y encabezados mal formados. Una URL de llaves aportada en el token nunca se consulta: la única fuente es el JWKS configurado para la instancia de Clerk.

Se exigen `iss` exacto, `sub` no vacío y `exp` presente y vigente. `nbf`, cuando esté presente, debe haber comenzado; un claim temporal presente pero mal formado siempre se rechaza. Se conserva una tolerancia de reloj de 5 segundos, con reloj inyectable para las pruebas.

En staging y producción la configuración exige una lista no vacía de partes autorizadas. `azp` debe existir y coincidir exactamente con un elemento de esa lista; no se aceptan comodines ni coincidencias de sufijo. Clerk puede omitir ese claim en ciertos contextos: esos tokens se rechazan en M2, y las integraciones sin sesión de navegador utilizarán llaves por espacio cuando exista HU-gateway-13. El modo de desarrollo usa su emisor local y una configuración explícitamente separada.

Si se configura una audiencia, `aud` debe contenerla, tanto en su forma de cadena como de lista de cadenas. La audiencia no sustituye `azp` ni permite usar claims de roles, organizaciones o planes para conceder permisos del kernel.

El token no puede superar 8192 bytes. No se registra su contenido, firma ni claims personales en los logs. Toda denegación de autenticación devuelve `unauthenticated` con un mensaje genérico. Un motivo interno puede registrarse como una categoría limitada (firma, emisor, vigencia, parte autorizada o llaves), sin material de la credencial.

### Caché y renovación

Valores iniciales de M2, configurables por entorno y comprobados con reloj inyectable:

| Parámetro | Valor |
|---|---|
| Tolerancia de reloj del token | 5 segundos |
| Caché fresca desde la última descarga válida | 5 minutos |
| Uso máximo de llaves desde la última descarga válida | 1 hora |
| Separación mínima entre intentos de renovación por emisor | 30 segundos |
| Tiempo máximo de la petición JWKS | 5 segundos, acotado también por el contexto |
| Tamaño máximo del documento JWKS | 1 MiB |

Una llave conocida dentro de la caché fresca verifica el token sin red. Un `kid` desconocido o una caché que deja de estar fresca provoca como máximo una renovación, respetando la separación mínima. Las peticiones concurrentes comparten el intento para que no multipliquen las descargas. Cada intento, exitoso o fallido, actualiza el instante del último intento; solo una respuesta válida actualiza el de la última descarga válida.

Si la descarga falla, una llave conocida puede seguir verificando tokens hasta una hora desde la última descarga válida, siempre con firma y claims vigentes. Una llave desconocida nunca obtiene ese fallback. Cumplida la hora se rechaza la autenticación hasta obtener un JWKS válido; los fallos de red no prolongan la vigencia.

Una respuesta válida reemplaza el conjunto de llaves: una llave retirada deja de aceptarse al renovar. Una respuesta vacía, inválida, demasiado grande o sin llaves admisibles es un fallo de renovación y no borra ni rejuvenece una caché válida. Los identificadores de llave duplicados se rechazan para evitar una selección ambigua.

Solo se aceptan llaves públicas RSA compatibles con RS256 y con módulo de al menos 2048 bits; si `use` o `alg` están presentes deben indicar firma y RS256. El cliente utiliza la URL HTTPS configurada en staging y producción; HTTP se limita a pruebas y desarrollo explícitos. No sigue redirecciones a otro origen ni incorpora destinos elegidos por el cliente.

### Alcance de implementación

HU-gateway-11 extiende el verificador y la fuente de llaves existentes, sin crear una segunda implementación independiente. HU-gateway-10 puede probar el gateway con un autenticador inyectado; la validación real se conecta con HU-gateway-11 y HU-gateway-12. El contrato y los manejadores del motor no cambian por este ADR.

## Alternativas consideradas

| Alternativa | Motivo para descartarla |
|---|---|
| Usar una llave conocida para siempre | Nunca se observa la retirada de la llave si no aparece un `kid` nuevo |
| Descargar llaves en cada solicitud | La disponibilidad del emisor pasa a ser requisito de cada operación |
| Renovar sin límite ante un `kid` desconocido | Permite convertir tokens falsos en descargas ilimitadas |
| Vaciar la caché ante cualquier fallo de red | Interrumpe sesiones válidas durante una caída breve del emisor |
| Aceptar tokens sin `azp` en producción | Elude la lista de orígenes autorizados que exige este corte de M2 |

## Consecuencias

- La autenticación tolera caídas limitadas del emisor y finalmente deja de confiar en una caché antigua.
- Una llave retirada puede seguir siendo aceptada hasta la siguiente renovación si el token sigue vigente; ante caída del emisor, el límite absoluto es una hora. Ese compromiso entre disponibilidad y revocación requiere aceptación del responsable.
- Los tokens de Clerk sin `azp` no sirven para el acceso de producción de M2. La prueba real debe validar que la instancia y el origen configurados producen el claim esperado.
- Los valores son política nueva y todavía no están implementados. El comportamiento del corte HTTP no cambia hasta HU-gateway-11.

## Cumplimiento

Pruebas antes del código, con servidor JWKS simulado: token válido; vencido; `nbf` futuro; firma ajena; emisor o parte autorizada incorrectos; `azp` ausente; audiencia configurada ausente o incorrecta; `alg=none` y simétrico; `kid` ausente o desconocido; rotación y retirada de llaves; intento compartido y límite de 30 segundos incluso sin caché inicial; caída con llave conocida dentro del máximo y rechazo al llegar a una hora; intento fallido que no renueva la antigüedad; documento inválido o excesivo; llave ambigua; y contexto cancelado. Ningún caso necesita una cuenta real de Clerk.

## Referencias

- [ADR-0036](0036-primera-puerta-http-del-kernel.md), [ADR-0038](0038-resolucion-de-actor-y-espacio.md), [ADR-0039](0039-identidad-en-el-primer-ingreso.md) y [backlog M2](../product/backlog/m2-puertas-del-kernel.md).
- Clerk: [claims de sesión y posible ausencia de azp](https://clerk.com/docs/guides/sessions/session-tokens) y [verificación de JWT](https://clerk.com/docs/guides/sessions/manual-jwt-verification).
