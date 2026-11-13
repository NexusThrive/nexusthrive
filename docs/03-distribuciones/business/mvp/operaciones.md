# Operaciones y señales del MVP de Business

## Propósito

Listar las operaciones del contrato `business.v1` y las señales que emiten.

## Alcance

Etapa 2. Las operaciones del kernel y de la plataforma están en [contrato.md](../../../02-arquitectura/contrato.md) y [consola.md](../../../02-arquitectura/plataforma/consola.md).

## Conceptos principales

Cada operación se declara en `contracts/proto/business/v1` con su permiso, su capacidad, su riesgo y si es idempotente. De ahí salen la API, el SDK y las herramientas MCP.

Riesgo: **L** lectura · **E** escritura · **$** mueve dinero (siempre idempotente).

## Funcionamiento

### Business base (`business.v1`)

| Operación | Permiso | Riesgo | Señal que emite |
|---|---|---|---|
| `onboarding.CreateBusiness` | sesión válida | E | `business.created.v1` |
| `onboarding.CompleteStep` | `business.setup` | E | `business.step_completed.v1` |
| `customers.Create` / `Update` | `customers.write` | E | `business.customer.created.v1` |
| `customers.List` / `Get` | `customers.read` | L | — |
| `catalog.UpsertItem` | `catalog.write` | E | `business.item.updated.v1` |
| `sales.Record` | `money.handle` | $ | `business.sale.recorded.v1` (+ `business.debt.opened.v1` si queda deuda) |
| `receivables.ReceivePayment` | `money.handle` | $ | `business.payment.received.v1` |
| `receivables.List` | `money.read` | L | — |
| `payments.CreateLink` | `money.handle` | $ | `business.payment_link.created.v1` |
| `cash.OpenSession` / `CloseSession` | `cash.handle` | $ | `business.cash.closed.v1` |
| `cash.RecordExpense` | `cash.handle` | $ | `business.expense.recorded.v1` |
| `reports.DailySummary` | `reports.read` | L | — |
| `team.Invite` / `SetRole` | `team.manage` | E | `business.member.invited.v1` |
| `capabilities.Request` | `business.setup` | E | `business.capability.requested.v1` |

### Patrón Membresía (`business.membership.v1`)

| Operación | Permiso | Riesgo | Señal |
|---|---|---|---|
| `plans.Upsert` | `catalog.write` | E | `business.plan.updated.v1` |
| `memberships.Assign` (con cobro opcional) | `money.handle` | $ | `business.membership.assigned.v1` |
| `memberships.Renew` | `money.handle` | $ | `business.membership.renewed.v1` |
| `memberships.Freeze` / `Unfreeze` | `members.write` | E | `business.membership.frozen.v1` |
| `memberships.Cancel` | `members.write` | E | `business.membership.canceled.v1` |
| `access.CheckIn` | `access.check` | E | `business.checkin.recorded.v1` |
| `access.IssueCredential` | `members.write` | E | `business.credential.issued.v1` |

### Señales del reloj (las emite el kernel solo)

| Señal | Cuándo | Quién la usa |
|---|---|---|
| `business.membership.expiring.v1` | 3 días antes del fin | n8n: recordatorio con enlace de pago |
| `business.membership.expired.v1` | Al pasar el fin | Acceso: rechaza la entrada |
| `business.debt.overdue.v1` | Al vencer una deuda | IA: lista de cobro sugerida |
| `business.trial.ending.v1` | 3 días antes de terminar la prueba | n8n: aviso al dueño |
| `business.day.closed.v1` | Fin del día del negocio | IA: resumen del día |

### Webhooks entrantes (adaptadores directos)

| Origen | Operación interna | Garantía |
|---|---|---|
| Wompi | `receivables.ReceivePayment` | Firma verificada; un evento nunca se aplica dos veces |

### Errores comunes del contrato

`permission_denied` · `capability_inactive` · `rule_violation` · `not_found` · `already_done` · `invalid_input` · `space_suspended`
