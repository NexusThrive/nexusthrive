# ADR-0038 · Actor verificado y resolución del espacio por slug

- **Estado:** Propuesta
- **Fecha:** 2026-11-15
- **Responsables:** santy401

## Contexto

HU-gateway-02 debe cerrar la selección del espacio antes de implementar HU-gateway-09 y HU-gateway-12. El ADR-0023 usa un slug para localizar el espacio; el corte HTTP del ADR-0036 usa su UUID porque la RLS impide buscar un espacio antes de fijar su identificador. La puerta no puede recibir el pool ni importar `kernel/state` para resolver esa diferencia.

`Engine.ResolveActor` ya resuelve una identidad verificada dentro de un espacio conocido. Falta un directorio acotado que encuentre el UUID desde el slug sin permitir enumerar espacios ni identidades. Los seguimientos 4, 6, 28, 29 y 46 exigen que los identificadores afirmados por un cliente nunca certifiquen al actor.

## Decisión propuesta

### Límite de confianza

La puerta valida la credencial antes de pedir resolución. Para Clerk entrega al motor el proveedor fijado por configuración y el `sub` del token validado, nunca un proveedor, identidad o participante elegidos desde el cuerpo de la operación. El resultado incluye el UUID del espacio, la identidad resuelta internamente y el `model.Actor` del participante activo. El `Actor` actual no necesita un campo público `IdentityID` para autenticar.

La ruta definitiva antepone `/{slug}` al procedimiento Connect. El slug solo localiza; no concede permisos. El motor conserva la variante de resolución por UUID para el corte de desarrollo existente hasta que HU-gateway-10 y HU-gateway-12 sustituyan su cableado. No se renombran espacios ni se implementan redirecciones de slugs en esta historia.

`ActorSystem` solo lo construyen los procesos internos. Un miembro del equipo de plataforma entra también con identidad validada y participante, sin convertirse en `system` por un claim, una cabecera o un campo de solicitud. Los accesos de soporte siguen pendientes de B4.

### Directorio mínimo

HU-gateway-12 añade una función interna `kernel.resolve_space_actor(provider, subject, slug)` que retorna como máximo una tupla `(space_id, identity_id, participant_id)`. Una única consulta exige simultáneamente el slug exacto, la identidad del proveedor y subject verificados, identidad habilitada y participante activo de ese espacio. No devuelve nombres, correos, listas, permisos ni información de un espacio sin pertenencia.

La función es `SECURITY DEFINER`, propiedad de un rol dedicado `nexusthrive_directory` sin inicio de sesión, superusuario, creación de roles o bases, replicación ni `BYPASSRLS`. Solo recibe SELECT sobre las columnas necesarias de `spaces`, `identities` y `participants`, y políticas SELECT exclusivas para ese rol que permiten la consulta del directorio. No recibe escrituras. La RLS normal del rol `nexusthrive_api` permanece intacta, con `FORCE ROW LEVEL SECURITY`.

El rol de la API no es miembro del rol del directorio y no puede usar `SET ROLE` para asumirlo. El único acceso adicional de la API es ejecutar la función exacta: se revoca EXECUTE de PUBLIC y se concede expresamente al rol de la API, en la misma migración. Se fija `search_path` a `pg_catalog`, se califican todos los objetos con su esquema y se prohíbe SQL dinámico. No se crean sobrecargas ni una variante que devuelva espacios sin identidad.

La función no prueba criptografía: el proceso de la API sigue siendo el límite de confianza que valida la credencial antes de llamarla. Quien controle sus credenciales de base puede afirmar un subject, igual que hoy puede afirmar un actor; esta decisión acota la consulta, no promete autenticar JWT dentro de PostgreSQL.

La resolución vive en `kernel/execution`, y el acceso SQL en `kernel/state`. No se concede ninguna nueva excepción de importación: solo esos dos paquetes pueden importar `kernel/state`. La puerta recibe el motor; nunca ve la función SQL, la conexión ni el pool.

Después de resolver, cada operación fija `space_id` en su propia transacción y vuelve a verificar participante, grants y estado del espacio con `Authorizer.Check`. La resolución previa no reemplaza esa verificación ni conserva una autorización que sobreviva a una revocación concurrente.

### Errores y límites

Slug ausente o inválido, identidad desconocida o deshabilitada, participante inexistente o archivado y slug ajeno devuelven el mismo `permission_denied`, con el mensaje público `the identity has no access to this space`. No se usa `not_found` para informar si existe el espacio. No se promete tiempo constante; se evitan consultas de existencia separadas y se prueba el mismo recorrido para resultados vacíos.

Los límites en memoria de HU-gateway-10 se aplican antes de la resolución, por IP y credencial validada; no se utiliza un identificador afirmado por el cuerpo para eludirlos. Los fallos de infraestructura producen un error interno genérico, no se presentan como ausencia de pertenencia. El contexto acota toda consulta.

No se crea `kernel/extension` en M2. `services/api` compone un único motor, sus operaciones y el autenticador; no recibe una ruta alternativa de resolución por `state`.

## Alternativas consideradas

| Alternativa | Motivo para descartarla |
|---|---|
| Buscar el slug desde la puerta con el pool | Rompe el límite del ADR-0035 y la prueba de arquitectura |
| Dar SELECT global o `BYPASSRLS` al rol de la API | Permite enumerar espacios fuera de la resolución acotada |
| Ejecutar el directorio como superusuario | Concede privilegios innecesarios a una función de solo lectura |
| Mantener UUID como única selección definitiva | Conserva el corte actual, pero no satisface HU-gateway-02 y HU-gateway-12 |

## Consecuencias

- La API selecciona espacios por slug sin quitar aislamiento a las consultas normales.
- Se introduce una excepción acotada de lectura entre espacios, protegida por una función y un rol interno. Debe revisarse como cambio de seguridad antes de aceptar este ADR.
- HU-gateway-12 requiere migración y pruebas de privilegios del rol del directorio. Su número se reserva al implementarla, coordinado con las migraciones de llaves y webhooks; no se reutiliza automáticamente el 00015.
- El rol adicional y sus políticas deben funcionar en la base local y en el proveedor de staging antes del despliegue. No se habilita un fallback con superusuario si el proveedor no permite prepararlos.

## Cumplimiento

Pruebas antes del código: mismo rechazo para slug inexistente y ajeno; dos identidades y dos espacios sin cruces; identidad deshabilitada y participante archivado; rechazo de autoridad afirmada por cuerpo o cabecera; revocación entre resolución y ejecución; ausencia de escrituras en la resolución; PUBLIC sin EXECUTE; API sin pertenencia al rol del directorio; privilegios y políticas limitados; búsqueda inmune a objetos temporales; y prueba de arquitectura sin nueva excepción a los importadores de `state`.

## Referencias

- [ADR-0006](0006-identidad.md), [ADR-0023](0023-urls-y-dominios.md), [ADR-0027](0027-roles-y-rls.md), [ADR-0035](0035-motor-de-ejecucion.md), [ADR-0036](0036-primera-puerta-http-del-kernel.md).
- [Seguimientos de M1](../ingenieria/sprint-01/seguimientos.md) y [HU-gateway-02](../product/backlog/m2-puertas-del-kernel.md).
- PostgreSQL 17: [seguridad por fila](https://www.postgresql.org/docs/17/ddl-rowsecurity.html) y [funciones SECURITY DEFINER](https://www.postgresql.org/docs/17/sql-createfunction.html).
