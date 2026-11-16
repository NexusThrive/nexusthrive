# ADR-0046 · Arranque con login limitado y verificador SCRAM

- **Estado:** Propuesta
- **Fecha:** 2026-11-16
- **Responsables:** santy401
- **Enmienda a:** ADR-0027, únicamente el mecanismo de login y provisión al arrancar

## Contexto

HU-gateway-20 exige migrar antes de abrir el puerto y probar un login real limitado, sin contraseñas en logs. El responsable indicó conservar `nexusthrive_api` como NOLOGIN porque es global al clúster y se comparte con otras bases y pruebas. El mecanismo antiguo `SetAPIRolePassword` intenta convertir ese mismo rol en LOGIN; no sirve para este entorno.

La API no debe permanecer con una conexión del dueño de las migraciones después del arranque. La nueva vía también permite probar el comportamiento local sin tocar la PostgreSQL ajena que usa el puerto 5432.

## Decisión propuesta

### Separación de roles

`nexusthrive_api` permanece NOLOGIN, sin propiedad de objetos ni privilegios elevados. No se le concede LOGIN ni siquiera desde el comando antiguo. Cada despliegue usa un rol de conexión distinto, con nombre bajo `nexusthrive_login_`, LOGIN, NOINHERIT, NOSUPERUSER, NOBYPASSRLS, NOCREATEDB, NOCREATEROLE y NOREPLICATION. Solo es miembro de `nexusthrive_api`, para ejecutar `SET ROLE nexusthrive_api` al conectar. No es miembro de los roles de directorio ni dueño de objetos.

El arranque rechaza un rol existente con atributos, membresías o propiedades incompatibles; no lo repara silenciosamente. El rol compartido se valida como NOLOGIN. Las pruebas crean y eliminan roles de conexión propios y únicos, sin cambiar los atributos del rol global.

### Contraseña y SCRAM

La contraseña de conexión es un secreto de configuración aleatorio de al menos 32 bytes, representado con caracteres ASCII, no una contraseña elegida por una persona. Go calcula el verificador SCRAM-SHA-256 con salt aleatorio de 16 bytes y las iteraciones configuradas por PostgreSQL, con mínimo de 4096. La contraseña en claro nunca se envía como texto de un `ALTER ROLE`; solo se envía el verificador.

El verificador también es material sensible: la aplicación no lo imprime en logs ni errores. Los registros administrativos de PostgreSQL pueden registrar DDL; calcular SCRAM evita enviar la contraseña clara, pero la protección de esos registros sigue siendo responsabilidad del operador. Los errores de provisión se redactan con mensajes fijos. El comando antiguo deja de habilitar el login del rol compartido. Una configuración incompleta falla antes de servir tráfico, sin anunciar éxito ni degradar a una conexión privilegiada.

### Fases del arranque

La imagen contiene el binario Go y los certificados requeridos, sin shell de desarrollo ni secretos incluidos. Su modo de bootstrap:

1. Abre únicamente la conexión de migración configurada y aplica todas las migraciones con el bloqueo previsto.
2. Provisiona o valida el login limitado y cierra la conexión privilegiada.
3. Comprueba el login real y su `SET ROLE` a `nexusthrive_api`.
4. Reejecuta el binario en modo de API con un entorno reducido, retirando la URL privilegiada y los secretos de provisión. Solo conserva las credenciales limitadas necesarias para el servicio.
5. Abre el puerto HTTP y arranca el programador ya implementado.

La reejecución reemplaza la memoria del proceso de bootstrap: la API no conserva un pool privilegiado. No se usa una shell para interpolar URLs o contraseñas. En el contenedor se ejecuta como usuario sin privilegios del sistema operativo.

La salud comprueba la conexión limitada con un plazo breve: responde 200 o 503 sin versiones, URLs ni errores internos. Un fallo de migración, provisión o login impide llegar al modo de API y abrir el puerto. El bootstrap no cambia el número de instancias ni añade coordinación distribuida.

### Configuración y alcance

La conexión de migración y la de ejecución se configuran por separado. Los nombres y valores de secretos se inyectan por entorno, nunca en la imagen ni el repositorio. El `.dockerignore` excluye archivos `.env` y credenciales locales del contexto de construcción. El modo de desarrollo existente conserva su función local; las pruebas nuevas ejercitan el login real con contraseña en el PostgreSQL del proyecto en 55432.

Esta decisión no autoriza desplegar en Render, Dokploy, Supabase ni Cloudflare. El responsable pidió completar primero desarrollo y pruebas locales. Una elección de alojamiento posterior debe respetar el programador único del ADR-0043.

## Alternativas consideradas

| Alternativa | Motivo para descartarla |
|---|---|
| Dar LOGIN al rol compartido | Contradice la restricción del responsable y afecta a otras bases del clúster |
| Mantener el dueño de migraciones como usuario de la API | Permite eludir los permisos y la RLS |
| Calcular la contraseña con SQL en el servidor | Puede dejar el texto claro en registros de consultas o DDL |
| Conservar el pool privilegiado después de migrar | Amplía la autoridad disponible al proceso que recibe peticiones |
| Ejecutar scripts de shell con secretos interpolados | Introduce problemas de quoting y exposición de credenciales |

## Consecuencias

- El login se prueba de verdad sin cambiar `nexusthrive_api` a LOGIN.
- El bootstrap necesita credenciales de migración solo durante la fase previa al servicio.
- Se añade una fase explícita de arranque y reejecución, conservando un solo binario de servicio.
- Requiere aceptación antes del código de bootstrap de HU-gateway-20; la imagen y la prueba local pertenecen a esa misma historia.

## Cumplimiento

Pruebas primero: login SCRAM real con contraseña correcta, rechazo de contraseña incorrecta, rol global siempre NOLOGIN, atributos y membresías limitados, RLS y esquema ajeno denegados, verificador diferente con salts nuevos, ausencia de contraseña y verificador en errores, migración fallida sin escuchar, entorno reducido al reejecutar, salud 503 con base caída e imagen sin archivos de secretos. Las cinco leyes y la suite completa siguen activas.

## Referencias

- [ADR-0027](0027-roles-y-rls.md), [ADR-0024](0024-herramienta-de-migraciones.md), [ADR-0043](0043-programador-en-la-api.md).
- [Backlog](../product/backlog/m2-puertas-del-kernel.md) y [traspaso](../ingenieria/traspaso-codex.md), seguimientos 1 y 2.
