# Despliegue

Cada pieza vive en su propio lugar. Cambiar una no obliga a tocar las demás.

```mermaid
flowchart TB
    U["Persona"] --> CDN["app.nexusthrive.com<br/>React estático en CDN"]
    U --> APP["App móvil y escritorio"]
    CDN -->|"HTTPS + token"| API["api.nexusthrive.com<br/>Kernel Go en contenedor"]
    APP -->|"HTTPS + token"| API
    CLK["Clerk"] -.->|"token"| U
    API --> PG[("PostgreSQL administrado")]
    API --> RD[("Redis")]
    PG -->|"réplica"| PYS["Servicio Python"]
    PYS -->|"MCP"| API
    API -->|"señales"| N8N["n8n"]
    N8N -->|"API"| API
```

| Pieza | Dónde | Notas |
|---|---|---|
| Caras web | CDN (Cloudflare Pages, Vercel o Netlify) | Solo archivos estáticos |
| Kernel | Contenedor (Fly.io, Railway, Render; luego AWS o GCP) | Único con acceso a la base |
| PostgreSQL | Administrado (Neon, Supabase o RDS) | Con réplica de lectura |
| Redis | Administrado | Caché y sesiones |
| Python | Contenedor aparte | Lee la réplica |
| n8n | Instancia propia | Habla con el kernel solo por la API |
| Móvil | App Store y Google Play vía Expo | Mismo SDK |
| Escritorio | Instaladores Tauri | Mismo SDK |
