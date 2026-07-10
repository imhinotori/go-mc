package server

// mth.go -- net.minecraft.util.Mth trig primitives, PORTED 1:1 from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, javap -c -p net.minecraft.util.Mth this session).
//
// Vanilla does NOT use java.lang.Math.sin/cos for gameplay trig -- it uses a 65536-entry lookup table
// (Mth.SIN) and a fast table-based atan2 (fastInvSqrt + ASIN_TAB/COS_TAB). Every mob-AI / projectile aim
// site that reads an angle (evoker fangs, the fired projectile's shoot-rotation) MUST go through these so
// the sub-block precision (which cell a fang spawns in near a boundary; the launch yaw/pitch) is bit-exact
// with Java. Re-expressed in idiomatic Go (no GPL paste); the table CONSTANTS + the arithmetic ops are
// literal. Cite net.minecraft.util.Mth.

import "math"

// mthSinTable is Mth.SIN: a 65536-entry float32 table filled by the static initializer's lambda$static$0:
//
//	SIN[i] = (float)Math.sin((double)i / 10430.378350470453d)   for i in [0, 65536)
//
// (10430.378350470453 == 65536 / (2*PI), so i/10430.378 == i*2*PI/65536.) VERIFIED javap Mth.<clinit>
// lambda$static$0: `i2d; ldc2_w 10430.378350470453; ddiv; Math.sin; d2f; fastore`.
var mthSinTable = func() [65536]float32 {
	var t [65536]float32
	for i := 0; i < 65536; i++ {
		t[i] = float32(math.Sin(float64(i) / mthSinScale))
	}
	return t
}()

// mthSinScale is the double constant 10430.378350470453 used by both the table FILL (divide) and the
// sin/cos LOOKUP (multiply). VERIFIED javap Mth.sin/cos: `ldc2_w 10430.378350470453; dmul`.
const mthSinScale = 10430.378350470453

// mthCosBias is the +16384.0 phase shift Mth.cos adds before the table index mask (cos(x) == sin(x + PI/2);
// 16384 == 65536/4). VERIFIED javap Mth.cos: `ldc2_w 16384.0; dadd`.
const mthCosBias = 16384.0

// mthRadToDeg is Mth.RAD_TO_DEG (public static final float): the degrees-per-radian factor Projectile.shoot
// multiplies the atan2 result by. Its double-widened value is exactly 57.2957763671875 (VERIFIED javap
// Projectile.shoot: `Mth.atan2(...); ldc2_w 57.2957763671875; dmul; d2f`). Cite Mth.RAD_TO_DEG.
const mthRadToDeg float32 = 57.2957763671875

// mthSin ports Mth.sin(double): SIN[(int)((long)(f*10430.378350470453) & 65535)].
//
//	VERIFIED javap Mth.sin: `getstatic SIN; dload_0; ldc2_w 10430.378350470453; dmul; d2l; ldc2_w 65535;
//	land; l2i; faload; freturn`.
func mthSin(f float64) float32 {
	return mthSinTable[int(int64(f*mthSinScale)&65535)]
}

// mthCos ports Mth.cos(double): SIN[(int)((long)(f*10430.378350470453 + 16384.0) & 65535)].
//
//	VERIFIED javap Mth.cos: `getstatic SIN; dload_0; ldc2_w 10430.378350470453; dmul; ldc2_w 16384.0;
//	dadd; d2l; ldc2_w 65535; land; l2i; faload; freturn`.
func mthCos(f float64) float32 {
	return mthSinTable[int(int64(f*mthSinScale+mthCosBias)&65535)]
}

// --- Mth.atan2 (the fast table-based approximation) ------------------------------------------------------

// mthFracBias is Mth.FRAC_BIAS = Double.longBitsToDouble(4805340802404319232L) == 17592186044416.0 (2^44 +
// 2^43 ... the magic bias that turns the [0,1] fraction into an integer table index via the mantissa bits).
// VERIFIED javap Mth.<clinit>: `ldc2_w 4805340802404319232; Double.longBitsToDouble; putstatic FRAC_BIAS`.
var mthFracBias = math.Float64frombits(4805340802404319232)

// mthAsinTab / mthCosTab are Mth.ASIN_TAB / Mth.COS_TAB: 257-entry double tables. For i in [0,256]:
//
//	d = i / 256.0; asin = Math.asin(d); COS_TAB[i] = Math.cos(asin); ASIN_TAB[i] = asin.
//
// VERIFIED javap Mth.<clinit>: `sipush 257; newarray double (x2); loop i<257 { i2d; ldc2_w 256.0; ddiv;
// Math.asin; dup; Math.cos; COS_TAB[i]=; ASIN_TAB[i]= }`.
var mthAsinTab, mthCosTab = func() ([257]float64, [257]float64) {
	var asinT, cosT [257]float64
	for i := 0; i < 257; i++ {
		d := float64(i) / 256.0
		as := math.Asin(d)
		cosT[i] = math.Cos(as)
		asinT[i] = as
	}
	return asinT, cosT
}()

// mthFastInvSqrt ports Mth.fastInvSqrt(double): the Quake-style inverse-square-root with one Newton step.
//
//	VERIFIED javap Mth.fastInvSqrt: `d1 = 0.5*x; bits = doubleToRawLongBits(x); bits = 6910469410427058090L
//	- (bits >> 1); x = longBitsToDouble(bits); x = x * (1.5 - d1*x*x); return x`.
func mthFastInvSqrt(x float64) float64 {
	half := 0.5 * x
	bits := int64(math.Float64bits(x))
	bits = 6910469410427058090 - (bits >> 1)
	y := math.Float64frombits(uint64(bits))
	y = y * (1.5 - half*y*y)
	return y
}

// mthAtan2 ports Mth.atan2(double y, double x): the table-based approximation (NOT java.lang.Math.atan2).
//
//	VERIFIED javap Mth.atan2: sq = x*x + y*y; if NaN -> NaN; negY = y<0 (negate y); negX = x<0 (negate x);
//	swap = y>x (swap x,y); invLen = fastInvSqrt(sq); x*=invLen; y*=invLen; biased = FRAC_BIAS + x;
//	idx = (int)doubleToRawLongBits(biased); asin = ASIN_TAB[idx]; cos = COS_TAB[idx]; frac = biased-FRAC_BIAS;
//	corr = (6.0 + frac*frac)*frac*0.16666666666666666; r = asin + (x*cos - y*frac ... );  <-- see below
//	if swap: r = PI/2 - r; if negX: r = PI - r; if negY: r = -r; return r.
//
// (Java arg order is atan2(double y, double x); the two locals dload_0=y, dload_2=x.)
func mthAtan2(y, x float64) float64 {
	// Mirror the JVM local slots exactly: d0 == dload_0 (param y), d2 == dload_2 (param x).
	d0, d2 := y, x
	sq := d2*d2 + d0*d0 // offsets 0-6: x*x + y*y
	if math.IsNaN(sq) {
		return math.NaN()
	}
	negY := d0 < 0.0 // offsets 21-32: dload_0 < 0
	if negY {         // offsets 34-41: negate dload_0
		d0 = -d0
	}
	negX := d2 < 0.0 // offsets 42-53: dload_2 < 0
	if negX {         // offsets 55-62: negate dload_2
		d2 = -d2
	}
	swap := d0 > d2 // offsets 63-74: dload_0 > dload_2
	if swap {        // offsets 76-88: swap dload_0 <-> dload_2
		d0, d2 = d2, d0
	}
	invLen := mthFastInvSqrt(sq) // offset 89
	d2 *= invLen                 // offsets 96-100
	d0 *= invLen                 // offsets 101-105
	// biased = FRAC_BIAS + dload_0 (offsets 106-111).
	biased := mthFracBias + d0
	// idx = (int) Double.doubleToRawLongBits(biased): the FRAC_BIAS trick puts the [0,1] fraction into the
	// low mantissa bits, so the l2i truncation yields the table index [0,256]. VERIFIED javap offsets
	// 113-119: doubleToRawLongBits(biased); l2i; istore 13.
	idx := int(int32(uint32(math.Float64bits(biased))))
	asinV := mthAsinTab[idx] // offsets 121-127
	cosV := mthCosTab[idx]   // offsets 129-135
	frac := biased - mthFracBias // offsets 137-143
	// d20 = dload_0*cos - dload_2*frac (offsets 145-154).
	d20 := d0*cosV - d2*frac
	// d22 = (6.0 + d20*d20) * d20 * 0.16666666666666666 (offsets 156-172).
	d22 := (6.0 + d20*d20) * d20 * 0.16666666666666666
	// d24 = asin + d22 (offsets 174-179).
	r := asinV + d22
	if swap { // offsets 181-192: r = PI/2 - r
		r = mthHalfPi - r
	}
	if negX { // offsets 194-205: r = PI - r
		r = math.Pi - r
	}
	if negY { // offsets 207-215: r = -r
		r = -r
	}
	return r
}
// mthHalfPi is the 1.5707963267948966d literal Mth.atan2 subtracts for the swap correction (VERIFIED javap
// offset 186: `ldc2_w 1.5707963267948966`).
const mthHalfPi = 1.5707963267948966
