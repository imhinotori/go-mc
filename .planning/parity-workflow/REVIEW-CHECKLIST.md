# Checklist de revisión e integración

## 1. Scope

- [ ] El diff toca exclusivamente los archivos autorizados.
- [ ] No hay cambios accidentales de formato, dependencias o generated data.
- [ ] No se pisaron cambios de Claude u otra ola.

## 2. Fidelidad JAR

- [ ] Clase y método existen en el JAR 26.2.
- [ ] Call chain preservado.
- [ ] Guards y early returns preservados.
- [ ] Casts float/double/int preservados.
- [ ] Constantes verificadas en bytecode, no copiadas de memoria/wiki.
- [ ] RNG source, número de draws y orden preservados.
- [ ] No se sustituyó un subsystem read por un literal.

## 3. Arquitectura

- [ ] Mutación ocurre en el owner correcto.
- [ ] No hay acceso cross-region directo.
- [ ] Barreras/intents resuelven IDs nuevamente antes de aplicar.
- [ ] No se añadió trabajo bloqueante al tick.
- [ ] CGO/dependencies permanecen dentro del contrato del proyecto.

## 4. Tests

- [ ] Test focalizado demuestra comportamiento, no sólo llamada/cobertura.
- [ ] Hay caso negativo/guard relevante.
- [ ] Si existe RNG, el test fija stream/draw order.
- [ ] Si existe wire, hay golden o decoder round-trip.
- [ ] Si existe persistencia, hay save→load→behavior round-trip.
- [ ] `git diff --check` verde.
- [ ] Suite focalizada verde.

## 5. Anti-fraude de paridad

- [ ] No apareció `TODO`, `DEFERRED`, `CITED STUB`, `approx`, `reduced`, `proxy` o `fallback` nuevo.
- [ ] No se cambió un test para aceptar el comportamiento Go actual.
- [ ] No se declaró `exact` una implementación parcial.
- [ ] El ledger apunta al test y al commit correcto.

## 6. Integración

- [ ] Revisión humana/central aprobada.
- [ ] Commit atómico creado después de la revisión.
- [ ] Integrado serialmente.
- [ ] Tests reejecutados sobre el branch integrado.
- [ ] Progress bar recalculada sólo si cambió cobertura observable.
