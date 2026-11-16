# HU-gateway-12 · Actor verificado y espacio por slug

## Propósito y alcance

Conectar el token validado con el participante y el espacio de las operaciones Connect, aplicando los ADR-0038 y 0039 aceptados. No crea identidades ni participantes durante el ingreso y no añade acceso de soporte de plataforma.

## Directorio

La migración `00015_actor_directory.sql` reserva el número 00015 para el directorio. Llaves y webhooks deberán escoger números posteriores. Crea el rol global `nexusthrive_directory` sin LOGIN, herencia, privilegios administrativos ni BYPASSRLS; rechaza un rol preexistente con atributos o asignaciones inseguros, sin repararlo silenciosamente.

El rol solo puede leer las columnas necesarias de espacios, identidades y participantes mediante tres políticas SELECT exclusivas. La función `kernel.resolve_space_actor(text,text,text)` es la única excepción SECURITY DEFINER del kernel, propiedad de ese rol y con `search_path=pg_catalog`. Todos los objetos se califican por esquema. Se revoca EXECUTE de PUBLIC y se concede a la API; la API no pertenece al rol del directorio. Las políticas normales y FORCE RLS de la API se conservan.

`state.LookupSpaceActor` hace una transacción de solo lectura y llama exclusivamente esa función. `Engine.ResolveActorForSlug` valida la forma del slug y recibe solo el proveedor fijo de Clerk y el sujeto ya verificado. Retorna UUID de espacio e identidad y el actor del participante activo. Espacio o identidad desconocidos, identidad deshabilitada y participante archivado producen la misma denegación pública.

La resolución previa no concede una autorización duradera: la operación verifica de nuevo participante, grants y estado en su transacción. Una revocación entre resolución y ejecución impide la operación.

## Cableado

`gateway.NewClerkAuthenticator` extrae el bearer token, lo verifica y pide la resolución únicamente al motor. Los identificadores y el tipo de actor no se obtienen de la petición. El identificador del contador de credenciales procede de la identidad resuelta en servidor.

`services/api` monta las rutas `/{slug}/kernel.v1.KernelService/...` con el autenticador real tanto en desarrollo como fuera de él. Se conserva un solo motor por proceso. El corte `/v1/spaces/{uuid}/...` y sus operaciones `ledger.*` quedan solo en desarrollo; en producción permanecen las operaciones del contrato y la salud. Las migraciones deben aplicarse antes de servir la versión nueva de la API.

## Pruebas y verificación

Las pruebas se escribieron antes de los métodos de resolución y del adaptador. Comprueban resolución correcta, ausencia de escrituras, errores iguales para identidad o espacio desconocidos, actor archivado, identidad deshabilitada y denegación después de revocar un grant. Se prueba la inmunidad a tablas temporales homónimas con un `search_path` hostil.

Las pruebas inspeccionan atributos y permisos del rol, políticas exclusivas de solo lectura, dueño y configuración de la función, ausencia de EXECUTE de PUBLIC y rechazo de cambio al rol desde una sesión autorizada como API. Esta última usa `SET SESSION AUTHORIZATION` porque `SET ROLE` por sí solo conserva un `session_user` superusuario en el arnés. No habilita LOGIN ni cambia contraseñas.

Las pruebas previas que exigían cero funciones SECURITY DEFINER ahora excluyen únicamente la firma exacta del directorio; cualquier otra excepción sigue fallando. Las políticas aplicables a la API conservan su prueba original. No se omiten leyes ni pruebas.

Las pruebas HTTP usan un token firmado de desarrollo sobre las rutas Connect reales, verifican denegaciones indistinguibles y ausencia de altas automáticas. El binario tiene una prueba de composición que confirma la ruta Connect, conserva salud y rechaza el corte de desarrollo fuera de ese modo.

## Referencias

Validación final: lint, suite completa (sin omitir arquitectura ni leyes), compilación de todos los módulos, enlaces locales, `git diff --check` y render de los 58 diagramas pasan. Las pruebas usan PostgreSQL en 55432. La integración en el sprint y la prueba con credenciales reales siguen pendientes.

- [ADR-0038](../../05-decisiones/0038-resolucion-de-actor-y-espacio.md), [ADR-0039](../../05-decisiones/0039-identidad-en-el-primer-ingreso.md) y [ADR-0040](../../05-decisiones/0040-validacion-de-clerk-y-cache-jwks.md).
- [Puertas](../../04-integraciones/puertas.md) y [modelo de datos](../../02-arquitectura/modelo-de-datos/kernel.md).
