# integrations

Notifications and third-party connections through n8n: WhatsApp, email, SMS, push, Telegram, Google and marketplaces.

- `n8n/nodes/`: the NexusThrive n8n node, built on `@nexusthrive/sdk`.
- `n8n/workflows/`: versioned workflows exported as JSON.

n8n listens to kernel signals and requests actions through the API. It never stores or moves value on its own. Payment gateways and e-invoicing are direct adapters, not n8n flows.
