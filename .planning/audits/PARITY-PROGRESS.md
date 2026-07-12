# Progress de paridad 1:1 — Minecraft Server 26.2

**Actualizado:** 2026-07-12
**Commit:** `88894a33eda6c412120b7813bd8b230b1826f67a`
**Referencia:** `temp/cache/26.2-inner.jar`
**Tipo de medición:** estimación ponderada por dominios observables
**Margen de incertidumbre:** ±6 puntos porcentuales; pendiente del censo multiworker de ola 00

## Barra global

```text
Paridad observable estimada
[████████████░░░░░░░░] 62%  (rango razonable: 56–68%)
```

Este porcentaje NO significa que el 57% de las clases del JAR esté portado. Mide cuánto de la experiencia observable está implementado con suficiente profundidad para acercarse a vanilla. Penaliza subsistemas amplios que existen pero todavía usan stubs, subsets, constantes o aproximaciones.

## Score por dominio

| Dominio | Peso | Score actual | Contribución | Motivo principal |
|---|---:|---:|---:|---|
| Protocolo, login, sesión y streaming | 10% | 70% | 7,0 | Core jugable; 29/70 serverbound packets siguen sin handler explícito |
| Chunks, lighting y ciclo de mundo | 12% | 72% | 8,64 | Relight funnel, status chain y scheduled ticks reales; quedan lifecycle/distances long-tail |
| Worldgen y estructuras | 15% | 60% | 9,0 | Noise/surface/features amplios; varias estructuras/providers conservan reducciones |
| Bloques, fluidos, física y redstone | 12% | 50% | 6,0 | Friction/speed-factor mejorados; neighbor updates, shapes, piston/redstone siguen parciales |
| Entidades, AI, Brain y spawning | 16% | 60% | 9,6 | Todas las categorías tienen cadence/pool; brains y roster efectivo siguen parciales |
| Combate, efectos, proyectiles y enchants | 10% | 72% | 7,2 | Keystone sólido y fixes recientes; todavía quedan effects/enchants/guards incompletos |
| Inventario, ítems, crafting y loot | 10% | 55% | 5,5 | Menús/recetas funcionales; componentes, loot functions y acciones mantienen gaps |
| Persistencia integral | 7% | 60% | 4,2 | AI-bearing mobs se reconstruyen; type-specific state y 8/111 components siguen parciales |
| Commands, advancements y gamerules | 5% | 65% | 3,25 | 59/59 gamerules y dificultad viva; commands/advancements siguen parciales |
| Región/concurrencia con semántica vanilla | 3% | 65% | 1,95 | Arquitectura/race discipline fuerte; feeding/breeding/knockback cross-region difieren |
| **Total ponderado** | **100%** | — | **62,34% ≈ 62%** | — |

## Contadores objetivos del snapshot

```text
Gamerules registradas:            59 / 59   = 100%
Atributos modelados:              40 / 40   = 100%
Serverbound handlers explícitos:  41 / 70   = 59%
Componentes con codec de disco:    8 / 111  =  7%
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

Estos avances mejoran dominios concretos, pero no cambian todavía los principales denominadores sistémicos.

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
Vanilla 26.2 parity: [████████████░░░░░░░░] 62% ±6
```
