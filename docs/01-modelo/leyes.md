# Las cinco leyes del kernel

Son las invariantes que nunca se rompen. De ellas sale la confianza: si el kernel las cumple, cualquier distribución encima es confiable.

## 1. Conservación

El valor no aparece ni desaparece; solo se mueve. Todo intercambio tiene origen y destino, y la suma cuadra.

*Es la contabilidad de doble partida, generalizada a cualquier recurso.*

## 2. Memoria

Lo que pasó no se reescribe. Un error se corrige con un evento nuevo que lo compensa, nunca borrando.

## 3. Compromiso

Toda promesa tiene dueño, plazo y estado, hasta que se cumple o se cancela. Ningún compromiso queda en el aire.

## 4. Permiso

Nadie actúa sin autoridad para hacerlo: ni una persona, ni una integración, ni un agente de IA. Toda acción queda atribuida a quien la hizo.

## 5. Movimiento

Con el tiempo, los compromisos despiertan acciones solos: recordar, cobrar, renovar, avisar. El sistema no espera a que alguien se acuerde.

## Cómo se garantizan

| Ley | Garantía técnica |
|---|---|
| Conservación | Cada intercambio se escribe en una sola transacción, con asientos que suman cero |
| Memoria | Tablas de eventos de solo agregar; correcciones como eventos compensatorios |
| Compromiso | Estado explícito (`abierto`, `cumplido`, `cancelado`) y vencimiento obligatorio |
| Permiso | Toda operación pasa por el motor de capacidades antes de ejecutarse |
| Movimiento | El reloj del kernel vigila los compromisos y emite señales al vencer |

Cada ley tendrá pruebas automáticas en `kernel/` que fallan si alguna pieza la rompe.
