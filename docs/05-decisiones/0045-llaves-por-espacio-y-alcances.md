# ADR-0045 · Llaves por espacio y alcances transaccionales

- **Estado:** Propuesta
- **Fecha:** 2026-11-16
- **Responsables:** santy401

## Contexto

HU-gateway-05 concreta R6 antes de HU-gateway-13, 14 y 19. El alcance aprobado de M2 incluye llaves con secreto visible una vez, hash, entorno `live`/`test`, alcances limitados, revocación y rotación. No existe aún `api_keys`. Autorizar solo al recibir la petición dejaría una carrera entre comprobar una llave y revocarla.

La reserva antigua de ADR-0041 ya pertenece a webhooks. La migración 00017 se reserva para reenvío y rotación de webhooks (HU-gateway-17); esta historia reserva 00018 para llaves. No se modifica el rol global de la API para darle LOGIN.

## Decisión propuesta

### Secreto y almacenamiento

La presentación será `ntk_<environment>_<uuid-v7-sin-guiones>_<secreto>`, donde el secreto son 32 bytes criptográficamente aleatorios codificados en base64url sin relleno. El identificador es público; conocerlo no autentica. `key_prefix` conserva solamente el prefijo hasta el identificador. `key_hash` guarda SHA-256 de la presentación completa como 32 bytes, con comparación en tiempo constante. La entropía procede del secreto aleatorio, no de una contraseña humana. La huella no permite recuperar la presentación.

Se muestra únicamente al crear o rotar, con `Cache-Control: no-store`. Esas operaciones no guardan una respuesta idempotente genérica, porque contendría el secreto. Una respuesta perdida se resuelve revocando y creando otra credencial o rotándola; la lectura nunca revela el secreto. Esto enmienda el criterio antiguo de HU-gateway-13 que permitía guardar la respuesta de creación por idempotencia.

`api_keys` tiene identificador UUID v7, espacio, participante, nombre, prefijo, huella, entorno, lista de alcances, vencimiento opcional, creación y revocación. La identidad se obtiene del participante activo ya existente; no se crean identidades o participantes automáticamente. `ip_allowlist` y el seguimiento de último uso se difieren: una autenticación de lectura no escribe metadatos por debajo del motor.

### Permisos y delegación

Se añaden `kernel.api_key.read` y `kernel.api_key.write`, riesgos `read` y `write`. El propietario de espacios nuevos los recibe; no se editan roles de espacios existentes. La creación, revocación y rotación requieren escritura; la consulta requiere lectura.

La llave pertenece a un participante activo del espacio con identidad activa. Puede ser la persona que la solicita o un agente ya provisionado en ese espacio. Al crearla, sus alcances deben ser un subconjunto tanto de los permisos efectivos del solicitante como de los del participante destinatario. No basta con poseer el permiso de administrar llaves para conceder privilegios que no se tienen. Una petición hecha con otra llave también queda limitada por los alcances de esa llave.

Al usarla se aplica la intersección entre sus alcances almacenados y los permisos vigentes de su participante. Los permisos que el participante recibe después no amplían los alcances de una llave existente; los que pierde dejan de servir de inmediato. La identidad, participante, espacio y alcances nunca proceden de campos afirmados por el cliente.

### Autenticación y revocación

La API exige configurar `API_KEY_ENVIRONMENT` como `test` o `live` cuando se habilita autenticación con llaves; una credencial del otro entorno se rechaza. No hay selección del entorno mediante una cabecera del cliente. Clerk sigue disponible para las personas.

El identificador de la presentación se busca mediante una función fija de directorio, SECURITY DEFINER, con dueño dedicado NOLOGIN, NOINHERIT y sin BYPASSRLS ni membresía para la API. Solo puede leer las columnas necesarias de llave, participante, identidad y espacio; nunca ofrece una consulta SQL arbitraria. El motor verifica la huella antes de devolver un actor; errores de identificador, hash, revocación, expiración, identidad o pertenencia responden con el mismo fallo de autenticación. La puerta recibe solo `Engine`, sin pool.

La llave determina su espacio. En Connect se comprueba además que coincida con el slug de la ruta. En `/mcp` basta la credencial: el espacio se resuelve de la llave validada y no se toma de argumentos de herramientas. Ninguna de las dos vías enumera espacios.

El motor lleva el identificador de credencial validada hasta la operación y vuelve a leerla dentro de la misma transacción que autoriza y actúa. Comprueba participante, entorno, revocación, vencimiento y cada alcance requerido. Para una escritura mantiene bloqueada la fila de llave hasta confirmar, de modo que una revocación o rotación concurrente espera a una operación ya autorizada; una operación posterior a su confirmación no usa la llave anterior. No hay caché de autorizaciones de llaves. Las operaciones `read` usan su instantánea de transacción, sin provocar escrituras ocultas.

Rotar crea una fila con identificador y secreto nuevos y revoca la anterior en la misma transacción, sin ventana de convivencia. Conserva el participante y el entorno, y no amplía alcances ni vencimiento. Revocar es irreversible; las filas se conservan como metadatos y no se borran eventos o auditoría. Rotar una llave ya revocada o vencida no la reactiva.

### Contratos y auditoría

Crear, consultar, revocar y rotar nacen en `contracts/`, con sus permisos, capacidad vacía, riesgo e idempotencia explícitos. Crear y rotar no son idempotentes por la presentación única del secreto. Revocar puede ser idempotente porque su respuesta no lo incluye. Los eventos de auditoría guardan identificadores y metadatos seguros, nunca presentación ni huella. Las credenciales no permiten llamar a operaciones internas ni construir un actor de sistema.

## Alternativas consideradas

| Alternativa | Motivo para descartarla |
|---|---|
| Texto plano o cifrado reversible para llaves de API | Solo se verifica un secreto; no hace falta recuperarlo como en HMAC de webhooks |
| Clave elegida por la persona | Introduce contraseñas de baja entropía y otro modelo de protección |
| Validar alcance solo en el gateway | Puede divergir del contrato o perder una revocación entre autenticación y escritura |
| Compartir la credencial de una persona con todos los agentes | Pierde la identidad y los permisos propios de cada participante |
| Guardar la respuesta de creación para repetición idempotente | Persiste el secreto mostrado una vez en otro registro |

## Consecuencias

- La revocación se aplica por transacción y los alcances no crecen por cambios posteriores de roles.
- Se añade una excepción de directorio estrecha y comprobada; el acceso habitual sigue sujeto a RLS por espacio.
- La gestión de llaves tiene dos permisos nuevos y no incluye interfaz, listas de IP ni emisión automática de identidades.
- Estos detalles requieren aceptación antes de implementar HU-gateway-13. El alcance de MCP de solo lectura ya está aceptado en ADR-0044.

## Cumplimiento

Pruebas primero: secreto de una sola presentación y ausencia en base/eventos/idempotencia/logs; hash alterado; igualdad de fallo para llave desconocida, revocada o de otro entorno; alcances fuera de permisos de solicitante o destinatario; reducción posterior de permisos; revocación y rotación concurrentes con una escritura; lectura de metadatos sin secretos; aislamiento, RLS y directorio sin privilegios generales. Paridad de contratos y cinco leyes siempre activas.

## Referencias

- [ADR-0035](0035-motor-de-ejecucion.md), [ADR-0038](0038-resolucion-de-actor-y-espacio.md), [ADR-0042](0042-secretos-y-firma-de-webhooks.md) y [ADR-0044](0044-mcp-de-solo-lectura-en-m2.md).
- [Modelo de datos](../02-arquitectura/modelo-de-datos/kernel.md), [puertas](../04-integraciones/puertas.md) y [backlog](../product/backlog/m2-puertas-del-kernel.md).
