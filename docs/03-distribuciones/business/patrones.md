# Patrones de Business

## Propósito

Definir las formas de operar de Business y qué rubros caben en cada una.

## Alcance

Los cinco patrones y sus pieles. La base común está en [README.md](README.md).

## Conceptos principales

Un **patrón** es una forma de operar con funciones propias. Una **piel** es un rubro sobre ese patrón: solo vocabulario y configuración.

Un rubro nuevo casi siempre es una piel nueva, sin código. El código solo crece cuando aparece un patrón realmente distinto.

## Funcionamiento

### 1. Membresía

Alguien paga un plan, tiene acceso por un tiempo y lo renueva.

- **Funciones propias:** planes, membresías, renovación, congelamiento, control de acceso (QR, NFC, documento), asistencia.
- **Pieles:** gimnasio, academia, yoga, crossfit, coworking.
- **En el kernel:** la membresía es un compromiso con vencimiento; el acceso es una regla que lee ese compromiso.

### 2. Productos

Alguien compra cosas que salen de un inventario.

- **Funciones propias:** variantes (talla, color), bodegas, existencias, listas de precios, pedidos, envíos.
- **Pieles:** tienda, merch, ferretería, distribuidora, e-commerce.
- **En el kernel:** el producto es un recurso; la venta es un intercambio que mueve dinero y existencias a la vez.

### 3. Preparación

Alguien pide algo que se prepara en el momento.

- **Funciones propias:** mesas, comandas, pantalla de cocina, recetas que descuentan insumos, cuenta dividida, propinas.
- **Pieles:** restaurante, café, bar, panadería.
- **En el kernel:** la comanda es un compromiso de entregar; al servir se cumple y la receta mueve insumos.

### 4. Servicio con agenda

Alguien reserva el tiempo de un profesional.

- **Funciones propias:** agenda, citas, profesionales, paquetes de sesiones, recordatorios.
- **Pieles:** belleza, barbería, salud, taller, consultoría.
- **En el kernel:** la cita es un compromiso con fecha; el tiempo del profesional es un recurso.

### 5. Proyecto

Alguien contrata algo grande que se entrega por partes.

- **Funciones propias:** presupuesto, análisis de precios unitarios, avance, actas, cobro por avance.
- **Pieles:** construcción, carpintería, consultoría por proyecto.
- **En el kernel:** el proyecto es un conjunto de compromisos; cada acta cumple una parte y genera un cobro.

## Reglas e invariantes

- Un patrón nuevo exige un ADR; una piel nueva solo una versión publicada desde la consola.
- Orden de construcción: Membresía (Gimnasio), Productos (Tienda), Preparación (Restaurante). Uno a la vez: el siguiente empieza cuando el anterior tiene negocios pagando.

## Decisiones relacionadas

- [ADR-0008 · Patrones con código, pieles con configuración](../../05-decisiones/0008-patrones-y-pieles.md)
