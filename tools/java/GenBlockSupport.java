import net.minecraft.SharedConstants;
import net.minecraft.core.BlockPos;
import net.minecraft.core.Direction;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.EmptyBlockGetter;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.SupportType;
import net.minecraft.world.level.block.state.BlockState;

import java.io.*;
import java.util.*;

/**
 * GenBlockSupport — Extracts the per-BLOCKSTATE block-support table that drives
 * net.minecraft.world.level.block.state.BlockBehaviour$BlockStateBase.isFaceSturdy 1:1.
 *
 * WHY this is a pure, codegen-able lookup (verified against the 26.2 jar this session):
 *   - BlockStateBase.isFaceSturdy(getter, pos, dir, type) is, when the per-state Cache is
 *     present, a pure table read: `return cache.isFaceSturdy(dir, type)`.
 *     CITE: javap BlockBehaviour$BlockStateBase.isFaceSturdy(...,SupportType) — when
 *     `cache != null` it does `getfield cache ; invokevirtual Cache.isFaceSturdy ; ireturn`.
 *   - The Cache constructor fills `boolean[] faceSturdy` (length DIRECTIONS.length *
 *     SUPPORT_TYPE_COUNT = 6 * 3 = 18) by iterating Direction.values() x SupportType.values()
 *     and calling `type.isSupporting(state, EmptyBlockGetter.INSTANCE, BlockPos.ZERO, dir)` —
 *     i.e. with NO world context. So the result is a pure function of the BlockState.
 *     CITE: javap BlockBehaviour$BlockStateBase$Cache.<init> — the nested loop ends in
 *     `getFaceSupportIndex(dir,type) ; type.isSupporting(state, EmptyBlockGetter.INSTANCE,
 *     BlockPos.ZERO, dir) ; bastore`.
 *   - getFaceSupportIndex(dir,type) = dir.ordinal()*SUPPORT_TYPE_COUNT + type.ordinal().
 *     CITE: javap Cache.getFaceSupportIndex — `Direction.ordinal ; SUPPORT_TYPE_COUNT ; imul ;
 *     SupportType.ordinal ; iadd ; ireturn`.
 *
 * We extract by simply CALLING the vanilla methods on every registered BlockState (the methods
 * already encapsulate the cache-vs-fallback decision), so the extracted values are vanilla-exact
 * even for hasDynamicShape() blocks (where the cache is null and isFaceSturdy falls through to a
 * live no-context isSupporting call). EmptyBlockGetter.INSTANCE + BlockPos.ZERO are the faithful
 * no-context arguments — exactly what the Cache constructor itself passes.
 *
 * Also extracted per state:
 *   - isSolid()  == the `legacySolid` field (BlockBehaviour$BlockStateBase.isSolid returns it).
 *                  This is the flag used by e.g. signs (`state.isSolid()`), NOT isFaceSturdy.
 *                  CITE: javap BlockStateBase.isSolid — `getfield legacySolid ; ireturn`.
 *   - isCollisionShapeFullBlock(EmptyBlockGetter.INSTANCE, BlockPos.ZERO) — the Cache field of
 *     the same name, = Block.isShapeFullBlock(state.getCollisionShape(...)). No-context arg.
 *     CITE: javap Cache.<init> — `state.getCollisionShape(EmptyBlockGetter, ZERO) ;
 *     Block.isShapeFullBlock ; putfield isCollisionShapeFullBlock`.
 *   - isSuffocating(EmptyBlockGetter.INSTANCE, BlockPos.ZERO) — the per-block isSuffocating
 *     StatePredicate evaluated with no world context. CITE: javap
 *     BlockBehaviour$BlockStateBase.isSuffocating — invokes the `isSuffocating` StatePredicate
 *     field (default = blocksMotion() && isCollisionShapeFullBlock(); some blocks override it).
 *     We bake the per-state result directly (the same way isFaceSturdy is baked) so the Go
 *     suffocation port is 1:1 for the dominant non-dynamic-shape blocks. The default predicate's
 *     isCollisionShapeFullBlock(getter,pos) is context-free for non-dynamic blocks, so
 *     EmptyBlockGetter+ZERO yields the faithful value.
 *
 * Output: block_support.json in the current directory.
 *
 * Structure (one row per BlockState, indexed by the GLOBAL block-state id
 * Block.getId(state) == Block.BLOCK_STATE_REGISTRY id — the SAME id the Go StateList uses):
 *   {"id": <int>, "mask": <int>}
 * where `mask` packs:
 *   bits 0..17  : faceSturdy[18], bit (dir.ordinal()*3 + type.ordinal())
 *   bit  18     : isSolid()  (legacySolid)
 *   bit  19     : isCollisionShapeFullBlock()
 *   bit  20     : isSuffocating()  (no-context)
 *
 * Rows are emitted in ascending id order; ids are dense (0..N-1).
 *
 * Direction.ordinal() order (CITE: javap Direction static-init putstatic sequence):
 *   DOWN=0, UP=1, NORTH=2, SOUTH=3, WEST=4, EAST=5.
 * SupportType.ordinal() order (CITE: javap SupportType field declaration order):
 *   FULL=0, CENTER=1, RIGID=2.
 */
public class GenBlockSupport {
    public static void main(String[] args) throws Exception {
        SharedConstants.tryDetectVersion();
        Bootstrap.bootStrap();

        // Direction in ordinal order (Direction.values() is ordinal-ordered).
        Direction[] dirs = Direction.values();
        SupportType[] types = SupportType.values();

        // Collect (id -> mask) for every registered block state.
        Map<Integer, Integer> byId = new TreeMap<>();

        for (Block block : BuiltInRegistries.BLOCK) {
            for (BlockState state : block.getStateDefinition().getPossibleStates()) {
                int id = Block.getId(state);

                int mask = 0;
                for (Direction dir : dirs) {
                    for (SupportType type : types) {
                        // EmptyBlockGetter.INSTANCE + BlockPos.ZERO == the Cache's own no-context
                        // args; for cache-null (dynamic-shape) blocks the vanilla method still
                        // computes the faithful no-context value via this same call.
                        boolean sturdy = state.isFaceSturdy(
                                EmptyBlockGetter.INSTANCE, BlockPos.ZERO, dir, type);
                        if (sturdy) {
                            // Mirror getFaceSupportIndex: dir.ordinal()*SUPPORT_TYPE_COUNT + type.ordinal().
                            int idx = dir.ordinal() * types.length + type.ordinal();
                            mask |= (1 << idx);
                        }
                    }
                }

                if (state.isSolid()) {
                    mask |= (1 << 18);
                }
                if (state.isCollisionShapeFullBlock(EmptyBlockGetter.INSTANCE, BlockPos.ZERO)) {
                    mask |= (1 << 19);
                }
                if (state.isSuffocating(EmptyBlockGetter.INSTANCE, BlockPos.ZERO)) {
                    mask |= (1 << 20);
                }

                byId.put(id, mask);
            }
        }

        // Sanity: ids must be dense 0..N-1 so the Go side can use a flat slice keyed by StateID.
        int n = byId.size();
        for (int i = 0; i < n; i++) {
            if (!byId.containsKey(i)) {
                System.err.printf("GenBlockSupport: WARNING non-dense state ids, missing id %d%n", i);
                break;
            }
        }

        try (PrintWriter pw = new PrintWriter(new FileWriter("block_support.json"))) {
            pw.println("[");
            int written = 0;
            for (Map.Entry<Integer, Integer> e : byId.entrySet()) {
                pw.printf("  {\"id\": %d, \"mask\": %d}%s%n",
                        e.getKey(), e.getValue(), written < n - 1 ? "," : "");
                written++;
            }
            pw.println("]");
        }

        System.out.printf("GenBlockSupport: wrote block_support.json (%d states)%n", n);
    }
}
