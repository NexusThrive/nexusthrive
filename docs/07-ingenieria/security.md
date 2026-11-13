# Seguridad

## Propósito

Definir las reglas de seguridad obligatorias en NexusThrive.

## Alcance

Código, datos, secretos, dependencias y operación. El acceso privilegiado del equipo está en [plataforma/seguridad.md](../02-arquitectura/plataforma/seguridad.md).

## Reglas e invariantes

### Datos

1. Aislamiento por espacio en todas las consultas, con RLS en PostgreSQL como segunda barrera.
2. Datos personales con consentimiento registrado y derecho a eliminación (Ley 1581 de 2012 de protección de datos en Colombia).
3. Cifrado en tránsito (TLS) y en reposo (el del proveedor de base de datos).
4. Llaves y tokens guardados solo como hash.

### Acceso

5. Toda operación verifica permiso en el servidor; ocultar un botón no es seguridad.
6. Doble factor obligatorio para el equipo NexusThrive.
7. Accesos de soporte temporales, con motivo y auditados.

### Código y dependencias

8. Sin secretos en el repositorio; `.env` está ignorado y existe `.env.example`.
9. Dependencias revisadas automáticamente (Dependabot) y actualizadas cada mes.
10. Webhooks entrantes con firma verificada y protección contra repetición.
11. Límites de peticiones por llave y por IP.

### Respuesta

12. Un incidente se registra, se contiene, se corrige y se documenta con sus causas.
13. Las copias de seguridad se restauran en prueba al menos una vez por trimestre.
