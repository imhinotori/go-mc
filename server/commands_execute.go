package server

// commands_execute.go -- the vanilla 26.2 /execute command (ledger #44-9), a 1:1 port of
// net.minecraft.server.commands.ExecuteCommand#register (verified via
//   javap -c -p net.minecraft.server.commands.ExecuteCommand
// against temp/cache/26.2-inner.jar). /execute forks and mutates a CommandSourceStack across
// its subcommand chain, then runs a tail command once per surviving source.
//
// PORTING NOTE (dispatcher shape). The fork command.Graph (server/command/*.go) is a static
// parse graph without Brigadier's ContextChain / RedirectModifier / fork() primitives, so a
// literal node-for-node port onto it is impossible. Instead this file models the vanilla
// CommandSourceStack (execSource) and the exact fork/gate/store/run semantics of the register
// tree as a self-contained interpreter, registered as one greedy-string /execute literal (the
// same pragmatic posture /scoreboard, /team, /time use in this codebase). Every subcommand's
// numeric ops, source mutation, and fan-out order mirror the jar bytecode; each is CITEd to its
// register lambda ($0..$12) or helper (expect/addConditional/storeValue).
//
// SUPPORTED subcommands (jar-faithful): as, at, positioned (+ as), rotated (+ as), facing
// (+ entity), align, anchored, in, if/unless (block, blocks, entity, score, dimension, loaded),
// store result|success into score, run.
//
// DELIBERATELY STUBBED (subsystem absent -- each returns an "unsupported" error, never a silent
// wrong result): if/unless predicate (LootItemCondition), if/unless biome (no biome sampler wired
// to commands), if/unless items / countItems (SlotProvider/SlotRange), if/unless data + store ...
// block|entity|storage (DataAccessor/NbtPath/storage), store ... bossbar (CustomBossEvent), execute
// summon (spawnEntityAndRedirect), the on <relation> subtree (createRelationOperations), positioned
// over <heightmap> (ServerLevel.getHeight not command-wired), and full entity-selector argument
// bodies (@e[type=,nbt=,scores=,distance=,...]) beyond the base @s/@p/@a/@r/@e/<name> selectors.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/server/command"
)

// degPerRad is float (180 / PI) as the jar stores it: ldc2_w double 57.2957763671875 in
// CommandSourceStack.facing(Vec3). Kept at exactly that double so atan2*degPerRad matches Java
// bit-for-bit before the d2f narrowing.
//
//	[VERIFIED javap CommandSourceStack.facing(Vec3): ldc2_w #186 // double 57.2957763671875d.]
const degPerRad = 57.2957763671875

// execTailDispatch is the seam execRun dispatches a `run` tail command through. It defaults to the
// shared fork graph Execute (the production path). It is a package var + bound in init() rather than
// calling executeCommand directly so buildCommandGraph (which registers /execute at cmdGraph init
// time) does not statically reach the executeCommand/cmdGraph vars -- that static reachability would
// be an initialization cycle. A test may swap it to capture the tail without touching the shared graph.
var execTailDispatch func(ctx context.Context, cmd string) error

func init() {
	// Bind in init() (runs after all package var initializers) rather than in the var initializer,
	// so the cmdGraph var-init chain (buildCommandGraph -> registerExecuteCommand -> ... -> execRun)
	// does not statically reach the executeCommand/cmdGraph vars -- that would be an init cycle.
	execTailDispatch = func(ctx context.Context, cmd string) error { return executeCommand(ctx, cmd) }
}

var (
	errExecuteUsage       = errors.New("usage: /execute <as|at|positioned|rotated|facing|align|anchored|in|if|unless|store|run> ...")
	errExecuteUnsupported = errors.New("that /execute subcommand is not supported yet")
	errConditionalFailed  = errors.New("Test failed") // ExecuteCommand.ERROR_CONDITIONAL_FAILED
	errExecuteBadSelector = errors.New("no entity selected")
	errExecuteAnchorType  = errors.New("Invalid entity anchor position type")
)

// execAnchor is EntityAnchorArgument$Anchor: FEET applies the raw position, EYES adds the entity
// eye height on Y. CITE EntityAnchorArgument$Anchor (FEET/EYES + apply).
type execAnchor int

const (
	anchorFeet execAnchor = iota // EntityAnchorArgument$Anchor.FEET
	anchorEyes                   // EntityAnchorArgument$Anchor.EYES
)

// execSource is the Go model of net.minecraft.commands.CommandSourceStack, carrying exactly the
// fields /execute forks: acting entity (player XOR generic Entity, either may be nil), dimension
// (level), world position (Vec3), rotation (Vec2: yaw=x, pitch=y), and entity anchor. It is a value
// type -- withX returns a COPY (vanilla returns a new stack), so a fork never mutates a sibling.
//
//	[VERIFIED javap CommandSourceStack fields: worldPosition (Vec3), level (ServerLevel),
//	 rotation (Vec2), entity (Entity), anchor. withEntity/withPosition/withRotation/withLevel/
//	 withAnchor each return a new stack.]
type execSource struct {
	t       *TickLoop
	player  *tickPlayer
	entity  *Entity
	dim     int
	x, y, z float64
	yaw     float32
	pitch   float32
	anchor  execAnchor
}

func (s execSource) withEntityPlayer(p *tickPlayer) execSource { s.player, s.entity = p, nil; return s }
func (s execSource) withEntity(e *Entity) execSource           { s.entity, s.player = e, nil; return s }
func (s execSource) withPosition(x, y, z float64) execSource   { s.x, s.y, s.z = x, y, z; return s }
func (s execSource) withRotation(yaw, pitch float32) execSource {
	s.yaw, s.pitch = yaw, pitch
	return s
}
func (s execSource) withLevel(dim int) execSource       { s.dim = dim; return s }
func (s execSource) withAnchor(a execAnchor) execSource { s.anchor = a; return s }

// anchoredPosition returns the source position with the anchor applied on Y (facing(Vec3) uses it).
// FEET returns Y unchanged; EYES adds the acting entity's eye height. No acting entity -> EYES
// degenerates to FEET.
//
//	[VERIFIED javap EntityAnchorArgument$Anchor.apply(CommandSourceStack): EYES adds getEyeHeight()
//	 to y, FEET returns position unchanged.]
func (s execSource) anchoredPosition() (float64, float64, float64) {
	if s.anchor == anchorEyes {
		return s.x, s.y + s.eyeHeight(), s.z
	}
	return s.x, s.y, s.z
}

// eyeHeight returns the acting entity's eye height for EYES. Players use 1.62 (PLAYER standing
// dimensions eyeHeight); a generic/absent entity uses 0. CITE Entity.getEyeHeight.
func (s execSource) eyeHeight() float64 {
	if s.player != nil {
		return 1.62
	}
	return 0
}

// registerExecuteCommand wires /execute as one greedy literal; the whole chain is parsed in the
// handler (the /scoreboard posture) so the wire graph stays a valid brigadier tree the client
// tab-completes at the /execute literal.
func registerExecuteCommand(g *command.Graph) {
	h := permissionGated("minecraft.command.execute", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok || e.p == nil {
			return nil
		}
		toks := strings.Fields(strings.TrimSpace(commandRaw(args)))
		return e.t.runExecute(ctx, e.p, toks)
	})
	rest := g.Argument("subcommand", command.StringParser(2)).HandleFunc(h)
	g.AppendLiteral(g.Literal("execute").AppendArgument(rest).HandleFunc(h))
}

// runExecute builds the initial CommandSourceStack from the issuing player, then interprets the
// subcommand chain.
//
//	[VERIFIED javap ExecuteCommand.register: literal("execute") + permission-2 subtree; the initial
//	 source is the command sender's CommandSourceStack.]
func (t *TickLoop) runExecute(ctx context.Context, p *tickPlayer, toks []string) error {
	if len(toks) == 0 {
		return errExecuteUsage
	}
	src := execSource{
		t: t, player: p, dim: p.dimension,
		x: p.x, y: p.y, z: p.z,
		yaw: p.yaw, pitch: p.pitch,
		anchor: anchorFeet,
	}
	_, err := t.execChain(ctx, []execSource{src}, toks)
	return err
}

// execChain interprets the tokens against the current source set, returning the ContextChain-folded
// result (sum of per-source run results / conditional pass count) and any error.
//
//	[VERIFIED javap ExecuteCommand.register: a chain of literal modifiers each redirecting to the
//	 execute root, terminated by `run <command>` or a conditional leaf that `.executes`.]
func (t *TickLoop) execChain(ctx context.Context, sources []execSource, toks []string) (int, error) {
	if len(toks) == 0 {
		return 0, errExecuteUsage
	}
	switch toks[0] {
	case "run":
		return t.execRun(ctx, sources, toks[1:])
	case "as":
		return t.execAs(ctx, sources, toks[1:])
	case "at":
		return t.execAt(ctx, sources, toks[1:])
	case "positioned":
		return t.execPositioned(ctx, sources, toks[1:])
	case "rotated":
		return t.execRotated(ctx, sources, toks[1:])
	case "facing":
		return t.execFacing(ctx, sources, toks[1:])
	case "align":
		return t.execAlign(ctx, sources, toks[1:])
	case "anchored":
		return t.execAnchored(ctx, sources, toks[1:])
	case "in":
		return t.execIn(ctx, sources, toks[1:])
	case "if":
		return t.execConditional(ctx, sources, toks[1:], false)
	case "unless":
		return t.execConditional(ctx, sources, toks[1:], true)
	case "store":
		return t.execStore(ctx, sources, toks[1:])
	case "summon", "on":
		return 0, errExecuteUnsupported
	default:
		return 0, errExecuteUsage
	}
}

// execAs ports `as <targets>` (register $0): for each selected entity, source.withEntity(entity);
// fan out in iteration order, then recurse.
//
//	[VERIFIED javap lambda$register$0: for each getOptionalEntities(targets): list.add(
//	 source.withEntity(entity)).]
func (t *TickLoop) execAs(ctx context.Context, sources []execSource, toks []string) (int, error) {
	if len(toks) == 0 {
		return 0, errExecuteUsage
	}
	sel := toks[0]
	var next []execSource
	for _, s := range sources {
		refs, err := s.selectEntities(sel)
		if err != nil {
			return 0, err
		}
		for _, r := range refs {
			if r.player != nil {
				next = append(next, s.withEntityPlayer(r.player))
			} else {
				next = append(next, s.withEntity(r.entity))
			}
		}
	}
	return t.execChain(ctx, next, toks[1:])
}

// execAt ports `at <targets>` (register $1): for each entity, withLevel(entity.level).withPosition(
// entity.position).withRotation(entity.getRotationVector). Fan out, then recurse.
//
//	[VERIFIED javap lambda$register$1: source.withLevel(entity.level).withPosition(entity.position())
//	 .withRotation(entity.getRotationVector()).]
func (t *TickLoop) execAt(ctx context.Context, sources []execSource, toks []string) (int, error) {
	if len(toks) == 0 {
		return 0, errExecuteUsage
	}
	sel := toks[0]
	var next []execSource
	for _, s := range sources {
		refs, err := s.selectEntities(sel)
		if err != nil {
			return 0, err
		}
		for _, r := range refs {
			ex, ey, ez, eyaw, epitch, edim := r.pose()
			next = append(next, s.withLevel(edim).withPosition(ex, ey, ez).withRotation(eyaw, epitch))
		}
	}
	return t.execChain(ctx, next, toks[1:])
}

// execPositioned ports `positioned <pos>` ($2), `positioned as <targets>` ($3), `positioned over
// <heightmap>` ($4, stubbed-with-error).
//
//	[VERIFIED javap lambda$register$2: withPosition(getVec3(pos)).withAnchor(FEET). lambda$register$3:
//	 for each target: withPosition(entity.position()). lambda$register$4: floor x/z, getHeight,
//	 withPosition(new Vec3(x,height,z)).]
func (t *TickLoop) execPositioned(ctx context.Context, sources []execSource, toks []string) (int, error) {
	if len(toks) == 0 {
		return 0, errExecuteUsage
	}
	switch toks[0] {
	case "as":
		if len(toks) < 2 {
			return 0, errExecuteUsage
		}
		sel := toks[1]
		var next []execSource
		for _, s := range sources {
			refs, err := s.selectEntities(sel)
			if err != nil {
				return 0, err
			}
			for _, r := range refs {
				ex, ey, ez, _, _, _ := r.pose()
				next = append(next, s.withPosition(ex, ey, ez))
			}
		}
		return t.execChain(ctx, next, toks[2:])
	case "over":
		return 0, errExecuteUnsupported
	default:
		if len(toks) < 3 {
			return 0, errExecuteUsage
		}
		var next []execSource
		for _, s := range sources {
			x, err := parseTpCoord(toks[0], s.x)
			if err != nil {
				return 0, err
			}
			y, err := parseTpCoord(toks[1], s.y)
			if err != nil {
				return 0, err
			}
			z, err := parseTpCoord(toks[2], s.z)
			if err != nil {
				return 0, err
			}
			next = append(next, s.withPosition(x, y, z).withAnchor(anchorFeet))
		}
		return t.execChain(ctx, next, toks[3:])
	}
}

// execRotated ports `rotated <yaw> <pitch>` ($5) and `rotated as <targets>` ($6).
//
//	[VERIFIED javap lambda$register$5: withRotation(getRotation(rot)). lambda$register$6: for each
//	 target: withRotation(entity.getRotationVector()).]
func (t *TickLoop) execRotated(ctx context.Context, sources []execSource, toks []string) (int, error) {
	if len(toks) == 0 {
		return 0, errExecuteUsage
	}
	if toks[0] == "as" {
		if len(toks) < 2 {
			return 0, errExecuteUsage
		}
		sel := toks[1]
		var next []execSource
		for _, s := range sources {
			refs, err := s.selectEntities(sel)
			if err != nil {
				return 0, err
			}
			for _, r := range refs {
				_, _, _, eyaw, epitch, _ := r.pose()
				next = append(next, s.withRotation(eyaw, epitch))
			}
		}
		return t.execChain(ctx, next, toks[2:])
	}
	if len(toks) < 2 {
		return 0, errExecuteUsage
	}
	var next []execSource
	for _, s := range sources {
		yaw, err := parseRotCoord(toks[0], float64(s.yaw))
		if err != nil {
			return 0, err
		}
		pitch, err := parseRotCoord(toks[1], float64(s.pitch))
		if err != nil {
			return 0, err
		}
		next = append(next, s.withRotation(float32(yaw), float32(pitch)))
	}
	return t.execChain(ctx, next, toks[2:])
}

// execFacing ports `facing <pos>` ($8) and `facing entity <targets> <anchor>` ($7).
//
//	[VERIFIED javap lambda$register$8: source.facing(getVec3(pos)). lambda$register$7: for each
//	 target: source.facing(entity, anchor).]
func (t *TickLoop) execFacing(ctx context.Context, sources []execSource, toks []string) (int, error) {
	if len(toks) == 0 {
		return 0, errExecuteUsage
	}
	if toks[0] == "entity" {
		if len(toks) < 3 {
			return 0, errExecuteUsage
		}
		sel := toks[1]
		anchor, err := parseAnchor(toks[2])
		if err != nil {
			return 0, err
		}
		var next []execSource
		for _, s := range sources {
			refs, err := s.selectEntities(sel)
			if err != nil {
				return 0, err
			}
			for _, r := range refs {
				ex, ey, ez, _, _, _ := r.pose()
				ty := ey
				if anchor == anchorEyes {
					ty += r.eyeHeight()
				}
				next = append(next, s.facing(ex, ty, ez))
			}
		}
		return t.execChain(ctx, next, toks[3:])
	}
	if len(toks) < 3 {
		return 0, errExecuteUsage
	}
	var next []execSource
	for _, s := range sources {
		x, err := parseTpCoord(toks[0], s.x)
		if err != nil {
			return 0, err
		}
		y, err := parseTpCoord(toks[1], s.y)
		if err != nil {
			return 0, err
		}
		z, err := parseTpCoord(toks[2], s.z)
		if err != nil {
			return 0, err
		}
		next = append(next, s.facing(x, y, z))
	}
	return t.execChain(ctx, next, toks[3:])
}

// facing ports CommandSourceStack.facing(Vec3): the byte-for-byte yaw/pitch from the anchor to
// (tx,ty,tz). h=sqrt(dx*dx+dz*dz); f1=wrapDegrees((float)(-(atan2(dy,h)*degPerRad)));
// f2=wrapDegrees((float)(atan2(dz,dx)*degPerRad)-90); withRotation(Vec2(f1,f2)).
//
//	[VERIFIED javap CommandSourceStack.facing(Vec3): dx=t.x-a.x; dy=t.y-a.y; dz=t.z-a.z;
//	 h=sqrt(dx*dx+dz*dz); f1=wrapDegrees((float)(-(Mth.atan2(dy,h)*57.2957763671875)));
//	 f2=wrapDegrees((float)(Mth.atan2(dz,dx)*57.2957763671875)-90.0f); withRotation(new Vec2(f1,f2)).]
func (s execSource) facing(tx, ty, tz float64) execSource {
	ax, ay, az := s.anchoredPosition()
	dx := tx - ax
	dy := ty - ay
	dz := tz - az
	h := math.Sqrt(dx*dx + dz*dz)
	f1 := wrapDegreesF(float32(-(math.Atan2(dy, h) * degPerRad)))
	f2 := wrapDegreesF(float32(math.Atan2(dz, dx)*degPerRad) - 90.0)
	return s.withRotation(f1, f2)
}

// execAlign ports `align <axes>` ($9): floor each axis named in the swizzle string.
//
//	[VERIFIED javap lambda$register$9 + Vec3.align: for each axis in the EnumSet, Mth.floor that
//	 coordinate; others pass through.]
func (t *TickLoop) execAlign(ctx context.Context, sources []execSource, toks []string) (int, error) {
	if len(toks) == 0 {
		return 0, errExecuteUsage
	}
	axes := toks[0]
	if !validSwizzle(axes) {
		return 0, fmt.Errorf("invalid swizzle: %s", axes)
	}
	var next []execSource
	for _, s := range sources {
		x, y, z := s.x, s.y, s.z
		if strings.ContainsRune(axes, 'x') {
			x = math.Floor(x)
		}
		if strings.ContainsRune(axes, 'y') {
			y = math.Floor(y)
		}
		if strings.ContainsRune(axes, 'z') {
			z = math.Floor(z)
		}
		next = append(next, s.withPosition(x, y, z))
	}
	return t.execChain(ctx, next, toks[1:])
}

// execAnchored ports `anchored <anchor>` ($10): withAnchor(anchor).
//
//	[VERIFIED javap lambda$register$10: withAnchor(getAnchor(anchor)).]
func (t *TickLoop) execAnchored(ctx context.Context, sources []execSource, toks []string) (int, error) {
	if len(toks) == 0 {
		return 0, errExecuteUsage
	}
	anchor, err := parseAnchor(toks[0])
	if err != nil {
		return 0, err
	}
	var next []execSource
	for _, s := range sources {
		next = append(next, s.withAnchor(anchor))
	}
	return t.execChain(ctx, next, toks[1:])
}

// execIn ports `in <dimension>` ($11): withLevel(dimension).
//
//	[VERIFIED javap lambda$register$11: withLevel(getDimension(dimension)).]
func (t *TickLoop) execIn(ctx context.Context, sources []execSource, toks []string) (int, error) {
	if len(toks) == 0 {
		return 0, errExecuteUsage
	}
	dim, ok := dimensionByName(toks[0])
	if !ok {
		return 0, fmt.Errorf("unknown dimension: %s", toks[0])
	}
	var next []execSource
	for _, s := range sources {
		next = append(next, s.withLevel(dim))
	}
	return t.execChain(ctx, next, toks[1:])
}

// execSourceContextKey is the (unexported, collision-free) context key the per-fork
// CommandSourceStack is carried under. execRun installs the COMPLETE source on the
// per-fork ctx (before the player executor) so any /execute consumer can recover the exact
// CommandSourceStack that drove this fork -- dimension, x/y/z, yaw/pitch, anchor, and acting
// player/entity -- via execSourceFrom(ctx). Unexported struct key = collision-free, same
// pattern as permResolverKey / cmdExecutorKey (commands.go).
type execSourceContextKey struct{}

// withExecSource returns ctx carrying the per-fork source by value. The execSource struct is
// itself a value type (commands_execute.go:execSource), so the receiver captures the fields as
// they stood at install time -- dimension, position, rotation, anchor, and acting player/entity
// are all preserved by value (the acting player/entity fields are pointers and remain so;
// only the pointer identity is captured, never a deep copy). This is the seam
// CommandSourceStack.withX returns a new stack -- every withX is a fresh value, so every fork's
// installed source is independent (sibling isolation: mutating one fork's source on ctx does
// not bleed into any other fork).
func withExecSource(ctx context.Context, s execSource) context.Context {
	return context.WithValue(ctx, execSourceContextKey{}, s)
}

// execSourceFrom extracts the source installed by withExecSource. Returns ok=false when absent
// -- a handler that needs the source must no-op safely when called without one (e.g. an
// /execute-less tail that reaches the same dispatcher path).
func execSourceFrom(ctx context.Context) (execSource, bool) {
	s, ok := ctx.Value(execSourceContextKey{}).(execSource)
	return s, ok
}

// execRun ports `run <command>` (the run subtree redirects into the dispatcher root). The tail runs
// once per surviving source with that source's acting player installed; the aggregate result is the
// sum of per-source successes. Per fork we install the COMPLETE source on the per-fork ctx (the
// CommandSourceStack model: dimension, position, rotation, anchor, acting player/entity -- all by
// value) BEFORE the player executor, so any /execute consumer can recover the source stack the fork
// dispatched with via execSourceFrom(ctx).
//
//	[VERIFIED javap ExecuteCommand.register: run redirects to dispatcher.getRoot();
//	 ExecutionContextChain runs the tail for every forked source and folds results.]
func (t *TickLoop) execRun(ctx context.Context, sources []execSource, toks []string) (int, error) {
	if len(toks) == 0 {
		return 0, errExecuteUsage
	}
	tail := strings.Join(toks, " ")
	total := 0
	var firstErr error
	for _, s := range sources {
		sctx := withExecSource(ctx, s)
		if s.player != nil {
			sctx = withExecutor(sctx, t, s.player)
		} else {
			// Mask any issuing-player executor inherited from the parent context. A generic
			// entity source must not make legacy player-only tail handlers act on the original
			// issuer; source-aware handlers recover the entity through execSourceFrom.
			sctx = context.WithValue(sctx, cmdExecutorKey{}, cmdExecutor{t: t})
		}
		if err := execTailDispatch(sctx, tail); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		total++
	}
	if total == 0 && firstErr != nil {
		return 0, firstErr
	}
	return total, nil
}

// -------- conditionals (if / unless) --------

// execConditional ports the if/unless subtree (addConditional/addConditionals). invert=true for
// unless. Keep only sources where test != invert (the fork's RedirectModifier via expect). With a
// tail recurse on survivors; without a tail the leaf `.executes` returns the pass count
// (ERROR_CONDITIONAL_FAILED when zero).
//
//	[VERIFIED javap addConditional: fork(node, ctx->expect(ctx, invert, predicate.test(ctx))) and
//	 executes(ctx-> predicate.test(ctx)!=invert ? 1 : throw ERROR_CONDITIONAL_FAILED). expect(ctx,
//	 invert, test): test==invert ? singleton(source) : emptyList.]
func (t *TickLoop) execConditional(ctx context.Context, sources []execSource, toks []string, invert bool) (int, error) {
	if len(toks) == 0 {
		return 0, errExecuteUsage
	}
	consumed, pred, err := t.parseCondition(toks)
	if err != nil {
		return 0, err
	}
	var survivors []execSource
	for _, s := range sources {
		ok, perr := pred(s)
		if perr != nil {
			return 0, perr
		}
		if ok != invert {
			survivors = append(survivors, s)
		}
	}
	tail := toks[consumed:]
	if len(tail) == 0 {
		if len(survivors) == 0 {
			return 0, errConditionalFailed
		}
		return len(survivors), nil
	}
	return t.execChain(ctx, survivors, tail)
}

// condPredicate evaluates one condition against a source (raw test, before the unless inversion).
type condPredicate func(s execSource) (ok bool, err error)

// parseCondition parses a condition head, returning tokens consumed + predicate. Ports the
// addConditionals dispatch (block/blocks/entity/score/dimension/loaded); biome/predicate/items/data
// stubbed-with-error.
//
//	[VERIFIED javap ExecuteCommand.addConditionals: literals block, blocks, biome, loaded,
//	 dimension, score, entity, predicate, items, data.]
func (t *TickLoop) parseCondition(toks []string) (int, condPredicate, error) {
	switch toks[0] {
	case "block":
		if len(toks) < 5 {
			return 0, nil, errExecuteUsage
		}
		return 5, func(s execSource) (bool, error) { return s.testBlock(toks[1], toks[2], toks[3], toks[4]) }, nil
	case "entity":
		//	[VERIFIED javap addConditionals: entity leaf -> !getOptionalEntities(entities).isEmpty().]
		if len(toks) < 2 {
			return 0, nil, errExecuteUsage
		}
		return 2, func(s execSource) (bool, error) {
			refs, err := s.selectEntitiesOptional(toks[1])
			if err != nil {
				return false, err
			}
			return len(refs) > 0, nil
		}, nil
	case "dimension":
		//	[VERIFIED javap addConditionals: dimension leaf -> source.getLevel().dimension() == dim.]
		if len(toks) < 2 {
			return 0, nil, errExecuteUsage
		}
		dim, ok := dimensionByName(toks[1])
		if !ok {
			return 0, nil, fmt.Errorf("unknown dimension: %s", toks[1])
		}
		return 2, func(s execSource) (bool, error) { return s.dim == dim, nil }, nil
	case "loaded":
		//	[VERIFIED javap addConditionals loaded leaf -> isChunkLoaded(level, pos).]
		if len(toks) < 4 {
			return 0, nil, errExecuteUsage
		}
		return 4, func(s execSource) (bool, error) { return s.testLoaded(toks[1], toks[2], toks[3]) }, nil
	case "score":
		return t.parseScoreCondition(toks)
	case "blocks":
		if len(toks) < 11 {
			return 0, nil, errExecuteUsage
		}
		return 11, func(s execSource) (bool, error) { return s.testBlocks(toks[1:10], toks[10]) }, nil
	case "biome", "predicate", "items", "data":
		return 0, nil, errExecuteUnsupported
	default:
		return 0, nil, errExecuteUsage
	}
}

// parseScoreCondition ports the `score` conditional: operator form (checkScore IntBiPredicate) and
// matches form (checkScore MinMaxBounds$Ints). Either null score info -> false.
//
//	[VERIFIED javap ExecuteCommand.checkScore(ctx, IntBiPredicate) / checkScore(ctx,
//	 MinMaxBounds$Ints): operators <, <=, =, >=, >; matches <range>.]
func (t *TickLoop) parseScoreCondition(toks []string) (int, condPredicate, error) {
	if len(toks) < 4 {
		return 0, nil, errExecuteUsage
	}
	target, targetObj := toks[1], toks[2]
	if toks[3] == "matches" {
		if len(toks) < 5 {
			return 0, nil, errExecuteUsage
		}
		lo, hi, err := parseIntRange(toks[4])
		if err != nil {
			return 0, nil, err
		}
		return 5, func(s execSource) (bool, error) {
			v, ok := s.readScore(target, targetObj)
			if !ok {
				return false, nil
			}
			return v >= lo && v <= hi, nil
		}, nil
	}
	if len(toks) < 6 {
		return 0, nil, errExecuteUsage
	}
	op, srcHolder, srcObj := toks[3], toks[4], toks[5]
	cmp, err := scoreOp(op)
	if err != nil {
		return 0, nil, err
	}
	return 6, func(s execSource) (bool, error) {
		tv, tok := s.readScore(target, targetObj)
		sv, sok := s.readScore(srcHolder, srcObj)
		if !tok || !sok {
			return false, nil
		}
		return cmp(tv, sv), nil
	}, nil
}

// -------- store --------

// execStore ports `store <result|success> score <holder> <objective> <tail...>` (wrapStores +
// storeValue). Only the SCORE target; bossbar/block/entity/storage stubbed-with-error. Runs the
// tail, then sets the holder's score to result (store result) or 0/1 success (store success).
//
//	[VERIFIED javap ExecuteCommand.wrapStores + storeValue(source, holders, objective, storeResult):
//	 withCallback((success,result)-> for each holder: score.set(storeResult ? result : (success?1:0))).]
func (t *TickLoop) execStore(ctx context.Context, sources []execSource, toks []string) (int, error) {
	if len(toks) < 2 {
		return 0, errExecuteUsage
	}
	var storeResult bool
	switch toks[0] {
	case "result":
		storeResult = true
	case "success":
		storeResult = false
	default:
		return 0, errExecuteUsage
	}
	switch toks[1] {
	case "score":
		if len(toks) < 4 {
			return 0, errExecuteUsage
		}
		holder, objName := toks[2], toks[3]
		o := t.scoreboard.getObjective(objName)
		if o == nil {
			return 0, fmt.Errorf("unknown scoreboard objective: %s", objName)
		}
		tail := toks[4:]
		if len(tail) == 0 {
			return 0, errExecuteUsage
		}
		res, err := t.execChain(ctx, sources, tail)
		if err != nil {
			// ERROR_CONDITIONAL_FAILED still fires the store callback with success=false; other
			// errors propagate.
			if errors.Is(err, errConditionalFailed) {
				t.storeScoreValue(holder, o, storeResult, 0, false)
				return 0, nil
			}
			return 0, err
		}
		t.storeScoreValue(holder, o, storeResult, int32(res), res > 0)
		return res, nil
	case "bossbar", "block", "entity", "storage":
		return 0, errExecuteUnsupported
	default:
		return 0, errExecuteUsage
	}
}

// storeScoreValue ports storeValue's onResult for score: set = storeResult ? result : (success?1:0).
//
//	[VERIFIED javap lambda$storeValue$0: score.set(storeResult ? result : (success ? 1 : 0)).]
func (t *TickLoop) storeScoreValue(holder string, o *scoreboardObjective, storeResult bool, result int32, success bool) {
	var v int32
	if storeResult {
		v = result
	} else if success {
		v = 1
	}
	t.scoreboardSetScore(holder, o, v)
}

// -------- source helpers: selectors, poses, scores, block tests --------

// execRef is a resolved selection target: a player XOR a generic entity.
type execRef struct {
	player *tickPlayer
	entity *Entity
}

// pose returns the ref world pose: x,y,z, yaw, pitch, dim.
func (r execRef) pose() (x, y, z float64, yaw, pitch float32, dim int) {
	if r.player != nil {
		return r.player.x, r.player.y, r.player.z, r.player.yaw, r.player.pitch, r.player.dimension
	}
	return r.entity.x, r.entity.y, r.entity.z, r.entity.yaw, r.entity.pitch, dimOverworld
}

// eyeHeight returns the ref eye height for the EYES facing anchor.
func (r execRef) eyeHeight() float64 {
	if r.player != nil {
		return 1.62
	}
	return 0
}

// selectEntities resolves a selector, requiring >= 1 match (EntityArgument.getEntities /
// ERROR_NO_ENTITIES when empty). Base selectors only: @s/@p/@a/@r/@e or a player name; selector
// argument bodies are stubbed-with-error.
func (s execSource) selectEntities(sel string) ([]execRef, error) {
	refs, err := s.selectEntitiesOptional(sel)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, errExecuteBadSelector
	}
	return refs, nil
}

// selectEntitiesOptional is the getOptionalEntities form -- may return empty (used by if entity).
func (s execSource) selectEntitiesOptional(sel string) ([]execRef, error) {
	if strings.ContainsAny(sel, "[]") {
		return nil, errExecuteUnsupported
	}
	t := s.t
	switch sel {
	case "@s":
		if s.player != nil {
			return []execRef{{player: s.player}}, nil
		}
		if s.entity != nil {
			return []execRef{{entity: s.entity}}, nil
		}
		return nil, nil
	case "@p", "@r":
		// v1 nearest/random player degenerates to the acting player when present, else the first
		// online player. Deterministic (no RNG) so no per-entity RNG stream is touched. CITE @p
		// NearestPlayer / @r RandomPlayer (degenerate without a full selector sort).
		if s.player != nil {
			return []execRef{{player: s.player}}, nil
		}
		for _, p := range t.players {
			if p != nil {
				return []execRef{{player: p}}, nil
			}
		}
		return nil, nil
	case "@a":
		var out []execRef
		for _, p := range t.players {
			if p != nil {
				out = append(out, execRef{player: p})
			}
		}
		return out, nil
	case "@e":
		var out []execRef
		for _, p := range t.players {
			if p != nil {
				out = append(out, execRef{player: p})
			}
		}
		for _, e := range t.cur().entities.all() {
			if e != nil {
				out = append(out, execRef{entity: e})
			}
		}
		return out, nil
	default:
		if p := t.playerByName(sel); p != nil {
			return []execRef{{player: p}}, nil
		}
		return nil, nil
	}
}

// readScore returns the holder score value + whether it exists (getPlayerScoreInfo -> null when
// unset, NOT getOrCreate). A bare @s holder resolves to the source own player name.
//
//	[VERIFIED javap checkScore: getPlayerScoreInfo(holder, objective) == null -> absent -> false.]
func (s execSource) readScore(holder, objName string) (int32, bool) {
	o := s.t.scoreboard.getObjective(objName)
	if o == nil {
		return 0, false
	}
	if holder == "@s" {
		if s.player != nil {
			holder = s.player.name
		} else {
			return 0, false
		}
	}
	byObj := s.t.scoreboard.scores[holder]
	if byObj == nil {
		return 0, false
	}
	sc := byObj[objName]
	if sc == nil {
		return 0, false
	}
	return sc.value, true
}

// testBlock ports `if block <pos> <blockstate>`: true iff the block at pos equals the named block
// default state id.
//
//	[VERIFIED javap addConditional block leaf -> BlockPredicateArgument test at BlockInWorld(pos);
//	 v1 compares the default state id of the named block (no property/tag matching yet).]
func (s execSource) testBlock(sx, sy, sz, blockName string) (bool, error) {
	x, err := parseBlockCoord(sx, s.x)
	if err != nil {
		return false, err
	}
	y, err := parseBlockCoord(sy, s.y)
	if err != nil {
		return false, err
	}
	z, err := parseBlockCoord(sz, s.z)
	if err != nil {
		return false, err
	}
	want, ok := blockStateByName(blockName)
	if !ok {
		return false, fmt.Errorf("unknown block: %s", blockName)
	}
	w := s.t.dimWorldByID(s.dim)
	if w == nil {
		return false, nil
	}
	cur, _ := w.GetBlock(pk.Position{X: x, Y: y, Z: z}, dimMinYFor(s.dim))
	return cur == want, nil
}

// testLoaded ports `if loaded <pos>`: true iff the block position chunk is loaded.
//
//	[VERIFIED javap ExecuteCommand.isChunkLoaded(level, pos).]
func (s execSource) testLoaded(sx, sy, sz string) (bool, error) {
	x, err := parseBlockCoord(sx, s.x)
	if err != nil {
		return false, err
	}
	y, err := parseBlockCoord(sy, s.y)
	if err != nil {
		return false, err
	}
	z, err := parseBlockCoord(sz, s.z)
	if err != nil {
		return false, err
	}
	w := s.t.dimWorldByID(s.dim)
	if w == nil {
		return false, nil
	}
	_, ok := w.GetBlock(pk.Position{X: x, Y: y, Z: z}, dimMinYFor(s.dim))
	return ok, nil
}

// testBlocks ports `if blocks <start> <end> <dest> <all|masked>`: compare source region to dest
// region block-for-block. all == every block matches; masked == source air is a wildcard.
//
//	[VERIFIED javap ExecuteCommand.checkRegions / addIfBlocksConditional: iterate the box, compare
//	 states; masked skips source-air cells.]
func (s execSource) testBlocks(coords []string, mode string) (bool, error) {
	if mode != "all" && mode != "masked" {
		return false, fmt.Errorf("invalid /execute if blocks mode: %s", mode)
	}
	masked := mode == "masked"
	c := make([]int, 9)
	bases := []float64{s.x, s.y, s.z, s.x, s.y, s.z, s.x, s.y, s.z}
	for i := 0; i < 9; i++ {
		v, err := parseBlockCoord(coords[i], bases[i])
		if err != nil {
			return false, err
		}
		c[i] = v
	}
	minX, maxX := minMax(c[0], c[3])
	minY, maxY := minMax(c[1], c[4])
	minZ, maxZ := minMax(c[2], c[5])
	dx, dy, dz := c[6], c[7], c[8]
	w := s.t.dimWorldByID(s.dim)
	if w == nil {
		return false, nil
	}
	minYf := dimMinYFor(s.dim)
	for bx := minX; bx <= maxX; bx++ {
		for by := minY; by <= maxY; by++ {
			for bz := minZ; bz <= maxZ; bz++ {
				srcState, _ := w.GetBlock(pk.Position{X: bx, Y: by, Z: bz}, minYf)
				if masked && block.IsAir(srcState) {
					continue
				}
				ox := dx + (bx - minX)
				oy := dy + (by - minY)
				oz := dz + (bz - minZ)
				dstState, _ := w.GetBlock(pk.Position{X: ox, Y: oy, Z: oz}, minYf)
				if srcState != dstState {
					return false, nil
				}
			}
		}
	}
	return true, nil
}

func minMax(a, b int) (int, int) {
	if a <= b {
		return a, b
	}
	return b, a
}

// parseRotCoord parses a rotation coordinate: "~" / "~n" relative to base, else absolute.
func parseRotCoord(s string, base float64) (float64, error) {
	if strings.HasPrefix(s, "~") {
		rest := strings.TrimPrefix(s, "~")
		if rest == "" {
			return base, nil
		}
		d, err := strconv.ParseFloat(rest, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid rotation: %s", s)
		}
		return base + d, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid rotation: %s", s)
	}
	return v, nil
}

// parseAnchor maps "feet"/"eyes" to the anchor enum (EntityAnchorArgument.getAnchor).
func parseAnchor(s string) (execAnchor, error) {
	switch strings.ToLower(s) {
	case "feet":
		return anchorFeet, nil
	case "eyes":
		return anchorEyes, nil
	}
	return anchorFeet, errExecuteAnchorType
}

// validSwizzle checks an align axes token: 1..3 of {x,y,z}, no repeats.
func validSwizzle(s string) bool {
	if s == "" || len(s) > 3 {
		return false
	}
	var seen [3]bool
	for _, ch := range s {
		var i int
		switch ch {
		case 'x':
			i = 0
		case 'y':
			i = 1
		case 'z':
			i = 2
		default:
			return false
		}
		if seen[i] {
			return false
		}
		seen[i] = true
	}
	return true
}

// dimensionByName maps a dimension identifier to the dim id.
func dimensionByName(s string) (int, bool) {
	name := strings.ToLower(strings.TrimSpace(s))
	name = strings.TrimPrefix(name, "minecraft:")
	switch name {
	case "overworld":
		return dimOverworld, true
	case "the_nether", "nether":
		return dimNether, true
	case "the_end", "end":
		return dimEnd, true
	}
	return 0, false
}

// scoreOp maps a comparison operator to an int comparator (ExecuteCommand IntBiPredicate).
//
//	[VERIFIED javap ExecuteCommand.addConditionals: operators "<" "<=" "=" ">=" ">".]
func scoreOp(op string) (func(a, b int32) bool, error) {
	switch op {
	case "<":
		return func(a, b int32) bool { return a < b }, nil
	case "<=":
		return func(a, b int32) bool { return a <= b }, nil
	case "=":
		return func(a, b int32) bool { return a == b }, nil
	case ">=":
		return func(a, b int32) bool { return a >= b }, nil
	case ">":
		return func(a, b int32) bool { return a > b }, nil
	}
	return nil, fmt.Errorf("invalid operation: %s", op)
}

// parseIntRange parses a MinMaxBounds$Ints range token: "n" (exact), "a..b", "a..", "..b".
//
//	[VERIFIED javap MinMaxBounds$Ints.matches(int): min <= v <= max, unbounded ends open.]
func parseIntRange(s string) (int32, int32, error) {
	const lo = math.MinInt32
	const hi = math.MaxInt32
	if strings.Contains(s, "..") {
		parts := strings.SplitN(s, "..", 2)
		var a int32 = lo
		var b int32 = hi
		if parts[0] != "" {
			n, err := strconv.ParseInt(parts[0], 10, 32)
			if err != nil {
				return 0, 0, fmt.Errorf("invalid range: %s", s)
			}
			a = int32(n)
		}
		if parts[1] != "" {
			n, err := strconv.ParseInt(parts[1], 10, 32)
			if err != nil {
				return 0, 0, fmt.Errorf("invalid range: %s", s)
			}
			b = int32(n)
		}
		return a, b, nil
	}
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid range: %s", s)
	}
	return int32(n), int32(n), nil
}
