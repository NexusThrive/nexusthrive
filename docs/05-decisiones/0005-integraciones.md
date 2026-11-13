# 0005 · Lo crítico va directo; lo demás por n8n

**Estado:** aceptada

## Decisión
- Pasarelas de pago y facturación electrónica: adaptadores directos de la distribución.
- Avisos y conexiones (WhatsApp, correo, push, Google, marketplaces): n8n, que escucha señales y pide acciones por la API.
- n8n nunca guarda ni mueve valor por su cuenta.

## Consecuencias
- Si n8n falla, solo se atrasan avisos; ningún cobro queda a medias.
- Pendiente: licencia comercial de n8n para usar credenciales de cada cliente.
