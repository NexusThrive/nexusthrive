# ADR-0009 · Libro de movimientos de doble partida como fuente de la verdad

- **Estado:** Aceptada
- **Fecha:** 2026-11-13
- **Responsables:** santy401

## Contexto

Guardar saldos, deudas y existencias en varias tablas produce cifras que no cuadran entre módulos.

## Decisión

Todo valor se mueve con intercambios (`exchanges`) cuyos asientos (`entries`) suman cero por recurso. Saldos, cartera, caja y existencias se derivan del libro. Las correcciones son intercambios compensatorios.

## Alternativas consideradas

| Alternativa | Por qué no se eligió |
|---|---|
| Saldos editables por módulo | Cifras inconsistentes y sin historia. |
| Contabilidad solo al final | Llega tarde y no sirve para operar. |

## Consecuencias

### Positivas

- Las cifras cuadran siempre y tienen historia completa.
- Sirve igual para dinero, inventario, tiempo o acceso.

### Negativas

- Consultas de saldo más costosas; se compensan con proyecciones en la misma transacción.

### Riesgos

- Errores en la traducción que generen asientos mal clasificados.

## Cumplimiento

Restricción diferida de suma cero en `kernel.entries` y pruebas de conservación en `kernel/laws/`.

## Referencias

- [Esquema kernel](../02-arquitectura/modelo-de-datos/kernel.md)
