# Progress de paridad 1:1 — Minecraft Server 26.2

**Actualizado:** 2026-07-12
**Commit:** `ba25c36f`
**Referencia:** `temp/cache/26.2-inner.jar`
**Tipo de medición:** estimación ponderada por dominios observables
**Margen de incertidumbre:** ±5 puntos porcentuales; gamerules, serverbound y components/block-entities ya censados por ola multiworker

## Barra global

```text
Paridad observable estimada
[████████████░░░░░░░░] 60%  (rango razonable: 55–65%)
```

Este porcentaje NO significa que el 57% de las clases del JAR esté portado. Mide cuánto de la experiencia observable está implementado con suficiente profundidad para acercarse a vanilla. Penaliza subsistemas amplios que existen pero todavía usan stubs, subsets, constantes o aproximaciones.

## Score por dominio

| Dominio | Peso | Score actual | Contribución | Motivo principal |
|---|---:|---:|---:|---|
| Protocolo, login, sesión y streaming | 10% | 58% | 5,8 | 69 paquetes reales + sentinel: 4 exactos, 33 parciales, 3 no-op explícitos y 30 default-noop |
| Chunks, lighting y ciclo de mundo | 12% | 72% | 8,64 | Relight funnel, status chain y scheduled ticks reales; quedan lifecycle/distances long-tail |
| Worldgen y estructuras | 15% | 60% | 9,0 | Noise/surface/features amplios; varias estructuras/providers conservan reducciones |
| Bloques, fluidos, física y redstone | 12% | 50% | 6,0 | Friction/speed-factor mejorados; neighbor updates, shapes, piston/redstone siguen parciales |
| Entidades, AI, Brain y spawning | 16% | 60% | 9,6 | Todas las categorías tienen cadence/pool; brains y roster efectivo siguen parciales |
| Combate, efectos, proyectiles y enchants | 10% | 72% | 7,2 | Keystone sólido y fixes recientes; todavía quedan effects/enchants/guards incompletos |
| Inventario, ítems, crafting y loot | 10% | 55% | 5,5 | Menús/recetas funcionales; componentes, loot functions y acciones mantienen gaps |
| Persistencia integral | 7% | 48% | 3,36 | 9 codecs BE auditados; 10 clases live-drive sin seam, 4 ausentes y sólo 8/111 components round-trip |
| Commands, advancements y gamerules | 5% | 55% | 2,75 | Registry 59/59; sólo 14 reglas tienen consumidor live-store, 7 siguen constantes y 38 sin consumidor |
| Región/concurrencia con semántica vanilla | 3% | 65% | 1,95 | Arquitectura/race discipline fuerte; feeding/breeding/knockback cross-region difieren |
| **Total ponderado** | **100%** | — | **59,8% ≈ 60%** | Recalibrado con censos de rutas observables; el código no retrocedió |

## Contadores objetivos del snapshot

```text
Gamerules registradas:            59 / 59   = 100%
Atributos modelados:              40 / 40   = 100%
Serverbound cases explícitos:     40 / 70   = 57% (incluye sentinel en el denominador generado)
Serverbound verificados exactos:   4 / 69   =  6% de paquetes reales
Componentes con codec de disco:    8 / 111  =  7%
Gamerules con consumidor live:     14 / 59   = 24%
Block entities auditadas con codec: 9; live-drive sin seam: 10; ausentes: 4
Natural spawn categories activas:  8 / 8    = 100% en cadence/pool wiring
Marcadores DEFERRED/CITED STUB:    1.048 ocurrencias
```

Los marcadores incluyen duplicados entre plugins/assets y comentarios históricos; sirven como señal de deuda, no como denominador exacto.

## Cambios integrados desde la auditoría inicial

- Breeze Slide locomotion.
- Multishot, Piercing y las dos curses en el effect engine.
- Raid captains, ominous banner, hero-of-the-village y ravager riders.
- Ender Dragon STRAFE DragonFireball y SITTING_FLAMING dragon breath.
- Corrección del light-mask de `ClientboundLightUpdate`.
- Ajuste del test de skylight superflat al comportamiento vanilla.
- Las 59 gamerules registradas y consumidores desconectados corregidos.
- Dificultad viva leída por los runtime sites.
- Reconstrucción del runtime AI-bearing de mobs al recargar chunks.
- Los 40 atributos vanilla y consumidores de dig/enchant conectados.
- Todas las categorías MobCategory cableadas al natural-spawn cadence.
- Scheduled ticks/header fields de chunks y BE tickers eager.

Estos avances mejoran dominios concretos, pero los censos mostraron que registro o dispatch no equivalen a paridad observable. Por eso la estimación baja de 62% preliminar a 60% con menor incertidumbre.

## Resultados multiworker incorporados

- [`census-gamerules.md`](../parity-workflow/outputs/census-gamerules.md): 59/59 registradas; 14 live-store, 7 constantes y 38 sin consumidor de producción.
- [`census-serverbound.md`](../parity-workflow/outputs/census-serverbound.md): 4 exactos, 33 parciales, 3 no-op explícitos y 30 default-noop sobre las 70 entradas generadas.
- [`census-persist-components-blockentities.md`](../parity-workflow/outputs/census-persist-components-blockentities.md): 8/111 components round-trip; 9 codecs BE auditados, 10 clases live-drive sin persistencia y 4 ausentes.
- El censo monolítico de persistencia se invalidó por timeout remoto. Player/chunk y entities/mobs permanecen pendientes y se ejecutarán como tareas separadas.

## Qué falta para que la barra sea objetiva

Debe existir un ledger machine-readable con una fila por unidad vanilla auditable:

| Campo | Ejemplo |
|---|---|
| Clase JAR | `net.minecraft.world.entity.animal.Pig` |
| Método | `registerGoals()` |
| Estado | `exact / partial / stub / absent / non-observable` |
| Implementación Go | `server/assets/vanilla_pig/main.star` |
| Evidencia JAR | comando `javap` o dump CFR usado |
| Test diferencial | nombre de test/oracle |
| RNG/order verificado | sí/no/n/a |
| Último commit verificado | hash |

Sin este ledger, los checkboxes de milestone pueden marcar scope reducido como completo aunque el requisito global 1:1 siga abierto.

## Reglas para mover el porcentaje

- Un archivo nuevo o un nuevo mob no aumenta automáticamente el score.
- `partial`, `proxy`, `same-region cut`, `const default` y `reduced` no cuentan como paridad completa.
- Un dominio sube sólo cuando el path observable está conectado y probado.
- Los tests deben comparar comportamiento, estado, wire, RNG/order o persistencia; no basta comprobar que una función fue llamada.
- Un fix puede bajar temporalmente el score si descubre una superficie antes ignorada. Eso hace la medición más honesta.

## Próximos hitos sugeridos

```text
60%  dificultad + 59 gamerules conectadas y tests de comportamiento
65%  persistencia completa de mobs + componentes de ítems sin pérdida común
70%  cross-region transparente + todas las categorías de natural spawn
80%  Brain/AI principales + bloques/redstone/estructuras sin reducciones mayores
90%  protocolo/commands/advancements/long-tail de mobs y componentes
100% cero desviaciones observables conocidas + oráculos diferenciales completos
```

## Barra resumida para README/reportes

```text
Vanilla 26.2 parity: [████████████░░░░░░░░] 60% ±5
```
