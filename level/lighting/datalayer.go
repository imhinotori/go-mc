// Package lighting is a 1:1 Go port of net.minecraft.world.level.lighting — the vanilla 26.2
// sky+block light engine. Every gameplay-observable value (light levels, BFS order, occlusion,
// nibble packing, level arithmetic, section padding) mirrors the unobfuscated jar
// method-for-method. Citations point at the ported class/method.
//
// PERMITTED OPTIMIZATION (documented once here): vanilla LayerLightSectionStorage keeps a
// double buffer — an `updatingSectionData` map mutated by the BFS and a `visibleSectionData`
// snapshot swapped in at the end of runLightUpdates so concurrent READERS see a consistent map
// while an update is in flight (ThreadedLevelLightEngine). This port runs the engine
// synchronously (the caller does not read light concurrently with an update), so the two buffers
// would always hold identical values at every read point. We therefore keep a SINGLE map and read
// from it directly. This changes nothing observable about the computed light values — it only
// removes the snapshot copy that exists purely for cross-thread read isolation. All value-affecting
// logic (getStoredLevel/setStoredLevel, section states, top-section tracking, queued data,
// storingLightForSection gating) is ported faithfully.
package lighting

// DataLayer ports net.minecraft.world.level.chunk.DataLayer: a 2048-byte nibble array holding 4096
// 4-bit values, indexed getIndex(x,y,z) = y<<8 | z<<4 | x. This is byte-identical to the layout of
// level.Section.SkyLight / .BlockLight, so a DataLayer's data slice IS a section light array on the
// wire. CITE: net.minecraft.world.level.chunk.DataLayer.
type DataLayer struct {
	// data is nil until first written (vanilla lazily allocates); a nil layer reads defaultValue.
	data         []byte
	defaultValue int
}

const dataLayerSize = 2048 // DataLayer.SIZE

// NewDataLayer ports DataLayer() (defaultValue 0, nil data). CITE: DataLayer.<init>().
func NewDataLayer() *DataLayer { return &DataLayer{} }

// NewDataLayerDefault ports DataLayer(int defaultValue). CITE: DataLayer.<init>(int).
func NewDataLayerDefault(defaultValue int) *DataLayer { return &DataLayer{defaultValue: defaultValue} }

// NewDataLayerBytes ports DataLayer(byte[] data) — wraps an existing 2048-byte array. CITE:
// DataLayer.<init>(byte[]).
func NewDataLayerBytes(data []byte) *DataLayer {
	if len(data) != dataLayerSize {
		panic("lighting: DataLayer should be 2048 bytes")
	}
	return &DataLayer{data: data}
}

// getIndex ports DataLayer.getIndex(x,y,z) = y<<8 | z<<4 | x. CITE: DataLayer.getIndex.
func dataLayerIndex(x, y, z int) int { return y<<8 | z<<4 | x }

func getByteIndex(index int) int   { return index >> 1 }   // DataLayer.getByteIndex(index) = index>>1
func getNibbleIndex(index int) int { return index & 1 }    // DataLayer.getNibbleIndex(index) = index&1

// Get ports DataLayer.get(x,y,z). CITE: DataLayer.get(int,int,int)/get(int).
func (d *DataLayer) Get(x, y, z int) int { return d.get(dataLayerIndex(x, y, z)) }

func (d *DataLayer) get(index int) int {
	if d.data == nil {
		return d.defaultValue
	}
	pos := getByteIndex(index)
	nibble := getNibbleIndex(index)
	// (data[pos] >> (4*nibble)) & 0xF — matches the signed-byte shift/mask in the jar.
	return int(d.data[pos]>>(4*nibble)) & 0xF
}

// Set ports DataLayer.set(x,y,z,val). CITE: DataLayer.set(int,int,int,int)/set(int,int).
func (d *DataLayer) Set(x, y, z, val int) { d.set(dataLayerIndex(x, y, z), val) }

func (d *DataLayer) set(index, val int) {
	data := d.getData()
	pos := getByteIndex(index)
	nibble := getNibbleIndex(index)
	// data[pos] = (data[pos] & ~(0xF << (4*nibble))) | ((val & 0xF) << (4*nibble))
	shift := 4 * nibble
	mask := byte(0xF << shift)
	data[pos] = (data[pos] &^ mask) | byte((val&0xF)<<shift)
}

// getData ports DataLayer.getData(): allocates the backing array on first write, filling it with
// the packed default value. CITE: DataLayer.getData + packFilled.
func (d *DataLayer) getData() []byte {
	if d.data == nil {
		d.data = make([]byte, dataLayerSize)
		if d.defaultValue != 0 {
			fill := packFilled(d.defaultValue)
			for i := range d.data {
				d.data[i] = fill
			}
		}
	}
	return d.data
}

// Data returns the backing 2048-byte array WITHOUT allocating (nil if never written). Used to hand
// the light array to the section/wire. Vanilla DataLayer.getData allocates on demand; the wire
// path only sends non-empty layers, so callers that need a concrete array call getData via Fill.
func (d *DataLayer) Data() []byte { return d.data }

// packFilled ports DataLayer.packFilled(int): a byte with the nibble value in both halves. CITE:
// DataLayer.packFilled.
func packFilled(value int) byte {
	b := byte(value & 0xF)
	return b | (b << 4)
}

// Fill ports DataLayer.fill(int): forces allocation and fills every nibble with value. CITE:
// DataLayer.fill.
func (d *DataLayer) Fill(value int) {
	data := d.getData()
	fill := packFilled(value)
	for i := range data {
		data[i] = fill
	}
}

// Copy ports DataLayer.copy(): a deep copy (shares nothing). A nil-data layer copies as nil-data.
// CITE: DataLayer.copy.
func (d *DataLayer) Copy() *DataLayer {
	if d.data == nil {
		return &DataLayer{defaultValue: d.defaultValue}
	}
	cp := make([]byte, dataLayerSize)
	copy(cp, d.data)
	return &DataLayer{data: cp, defaultValue: d.defaultValue}
}

// IsEmpty ports DataLayer.isEmpty(): true iff no backing array has been allocated. CITE:
// DataLayer.isEmpty.
func (d *DataLayer) IsEmpty() bool { return d.data == nil }

// IsDefinitelyHomogenous ports DataLayer.isDefinitelyHomogenous() = `this.data == null`. CITE:
// DataLayer.isDefinitelyHomogenous.
func (d *DataLayer) IsDefinitelyHomogenous() bool { return d.data == nil }
