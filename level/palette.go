package level

import (
	"io"
	"strconv"

	"github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

type State interface {
	~int
}
type (
	BlocksState = block.StateID
	BiomesState = biome.Type
)

type PaletteContainer[T State] struct {
	bits    int
	config  paletteCfg[T]
	palette palette[T]
	data    *BitStorage
}

func NewStatesPaletteContainer(length int, defaultValue BlocksState) *PaletteContainer[BlocksState] {
	return &PaletteContainer[BlocksState]{
		bits:    0,
		config:  statesCfg{},
		palette: &singleValuePalette[BlocksState]{v: defaultValue},
		data:    NewBitStorage(0, length, nil),
	}
}

// NewStatesPaletteContainerFromSave builds a block-state container from an ANVIL section, where
// the data array holds PALETTE INDICES into an explicit palette (never raw state ids — Anvil has
// no direct format). It resolves each index to its real state id and rebuilds the IN-MEMORY
// container in the same representation generation produces, so the subsequent network WriteTo
// emits the correct wire format:
//   - <=256 distinct states -> a linear/hash palette (indexed), as before;
//   - >256 distinct states  -> the globalPalette with RAW state ids in the data (the direct
//     format the 26.2 client expects at bits>=9).
//
// BUG-5: the old reload used the NETWORK constructor, which at bits>=9 built a globalPalette but
// LEFT the data holding Anvil palette INDICES. globalPalette.value(i)=i returned the index as the
// state id, so a >256-entry section read back garbage (and the next chunk send crashed the client
// with IndexOutOfBounds in PalettedContainer.read). Resolving indices->state ids here fixes both
// the in-memory reads and the wire encode.
func NewStatesPaletteContainerFromSave(length int, data []uint64, pat []BlocksState) *PaletteContainer[BlocksState] {
	resolved := resolveAnvilCells(length, data, pat)
	c := NewStatesPaletteContainer(length, 0)
	if len(pat) == 1 {
		c.palette = &singleValuePalette[BlocksState]{v: pat[0]}
		return c
	}
	for i, st := range resolved {
		c.Set(i, st)
	}
	return c
}

// NewBiomesPaletteContainerFromSave is the biome analogue of NewStatesPaletteContainerFromSave.
func NewBiomesPaletteContainerFromSave(length int, data []uint64, pat []BiomesState) *PaletteContainer[BiomesState] {
	resolved := resolveAnvilCells(length, data, pat)
	c := NewBiomesPaletteContainer(length, 0)
	if len(pat) == 1 {
		c.palette = &singleValuePalette[BiomesState]{v: pat[0]}
		return c
	}
	for i, st := range resolved {
		c.Set(i, st)
	}
	return c
}

// resolveAnvilCells expands an Anvil (palette, indexData) section into the per-cell value list by
// reading each cell's palette index out of the data BitStorage and mapping it through the palette.
// The index width is derived from the data length (calcBitsPerValue) exactly as the network read
// does. A single-entry (or empty-data) palette yields every cell = pat[0] (or the zero value).
func resolveAnvilCells[T State](length int, data []uint64, pat []T) []T {
	out := make([]T, length)
	if len(pat) <= 1 {
		if len(pat) == 1 {
			for i := range out {
				out[i] = pat[0]
			}
		}
		return out
	}
	bits := calcBitsPerValue(length, len(data))
	bs := NewBitStorage(bits, length, data)
	for i := 0; i < length; i++ {
		idx := bs.Get(i)
		if idx >= 0 && idx < len(pat) {
			out[i] = pat[idx]
		}
	}
	return out
}

func NewStatesPaletteContainerWithData(length int, data []uint64, pat []BlocksState) *PaletteContainer[BlocksState] {
	var p palette[BlocksState]
	n := calcBitsPerValue(length, len(data))
	switch n {
	case 0:
		p = &singleValuePalette[BlocksState]{pat[0]}
	case 1, 2, 3, 4:
		n = 4
		p = &linearPalette[BlocksState]{
			values: pat,
			bits:   n,
		}
	case 5, 6, 7, 8:
		ids := make(map[BlocksState]int)
		for i, v := range pat {
			ids[v] = i
		}
		p = &hashPalette[BlocksState]{
			ids:    ids,
			values: pat,
			bits:   n,
		}
	default:
		p = &globalPalette[BlocksState]{}
	}
	return &PaletteContainer[BlocksState]{
		bits:    n,
		config:  statesCfg{},
		palette: p,
		data:    NewBitStorage(n, length, data),
	}
}

func NewBiomesPaletteContainer(length int, defaultValue BiomesState) *PaletteContainer[BiomesState] {
	return &PaletteContainer[BiomesState]{
		bits:    0,
		config:  biomesCfg{},
		palette: &singleValuePalette[BiomesState]{v: defaultValue},
		data:    NewBitStorage(0, length, nil),
	}
}

func NewBiomesPaletteContainerWithData(length int, data []uint64, pat []BiomesState) *PaletteContainer[BiomesState] {
	var p palette[BiomesState]
	n := calcBitsPerValue(length, len(data))
	switch n {
	case 0:
		p = &singleValuePalette[BiomesState]{pat[0]}
	case 1, 2, 3:
		p = &linearPalette[BiomesState]{
			values: pat,
			bits:   n,
		}
	default:
		p = &globalPalette[BiomesState]{}
	}
	return &PaletteContainer[BiomesState]{
		bits:    n,
		config:  biomesCfg{},
		palette: p,
		data:    NewBitStorage(n, length, data),
	}
}

func (p *PaletteContainer[T]) Get(i int) T {
	return p.palette.value(p.data.Get(i))
}

func (p *PaletteContainer[T]) Set(i int, v T) {
	if vv, ok := p.palette.id(v); ok {
		p.data.Set(i, vv)
	} else {
		length := p.data.Len()
		// resize. The palette.id() miss returns the *requested* bit width (vv);
		// the actual stored width is config.bits(vv) (e.g. states floor 1..4 -> 4,
		// >=9 -> the global-palette width). The container's bits field, the data
		// BitStorage, AND the wire header must all use that floored width — writing
		// the raw vv as the header bits-per-entry while packing config.bits(vv)-wide
		// longs misframes the section for a vanilla client (the 776 stripes/void
		// bug the capture-diff caught: header said 1 bit, data was 4-bit/256 longs).
		storedBits := p.config.bits(vv)
		newContainer := PaletteContainer[T]{
			bits:    storedBits,
			config:  p.config,
			palette: p.config.create(vv),
			data:    NewBitStorage(storedBits, length, nil),
		}
		// copy
		for i := 0; i < length; i++ {
			newContainer.Set(i, p.Get(i))
		}

		if vv, ok := newContainer.palette.id(v); !ok {
			panic("not reachable")
		} else {
			newContainer.data.Set(i, vv)
		}
		*p = newContainer
	}
}

func (p *PaletteContainer[T]) ReadFrom(r io.Reader) (n int64, err error) {
	var nBits pk.UnsignedByte
	n, err = nBits.ReadFrom(r)
	if err != nil {
		return
	}
	p.bits = p.config.bits(int(nBits))
	p.palette = p.config.create(int(nBits))

	nn, err := p.palette.ReadFrom(r)
	n += nn
	if err != nil {
		return n, err
	}

	// Protocol 770+ (MC 1.21.5+): data array has no VarInt length prefix.
	// Compute expected long count from bits and container capacity.
	dataLen := calcBitStorageSize(p.bits, p.data.length)
	if cap(p.data.data) >= dataLen {
		p.data.data = p.data.data[:dataLen]
	} else {
		p.data.data = make([]uint64, dataLen)
	}
	var v pk.Long
	for i := range p.data.data {
		nn, err = v.ReadFrom(r)
		n += nn
		if err != nil {
			return n, err
		}
		p.data.data[i] = uint64(v)
	}
	return n, p.data.Fix(p.bits)
}

func (p *PaletteContainer[T]) WriteTo(w io.Writer) (n int64, err error) {
	n, err = pk.Tuple{
		pk.UnsignedByte(p.bits),
		p.palette,
	}.WriteTo(w)
	if err != nil {
		return n, err
	}
	// Protocol 770+ (MC 1.21.5+): write data longs without VarInt length prefix.
	for _, v := range p.data.Raw() {
		nn, err := pk.Long(v).WriteTo(w)
		n += nn
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// Palette export the raw palette values for @maxsupermanhd.
// Others shouldn't call this because this might be removed
// after max doesn't need it anymore.
func (p *PaletteContainer[T]) Palette() []T {
	return p.palette.export()
}

type paletteCfg[T State] interface {
	bits(int) int
	create(bits int) palette[T]
}

type statesCfg struct{}

func (s statesCfg) bits(bits int) int {
	switch bits {
	case 0:
		return 0
	case 1, 2, 3, 4:
		return 4
	case 5, 6, 7, 8:
		return bits
	default:
		return block.BitsPerBlock
	}
}

func (s statesCfg) create(bits int) palette[BlocksState] {
	switch bits {
	case 0:
		return &singleValuePalette[BlocksState]{v: -1}
	case 1, 2, 3, 4:
		return &linearPalette[BlocksState]{bits: 4, values: make([]BlocksState, 0, 1<<4)}
	case 5, 6, 7, 8:
		return &hashPalette[BlocksState]{
			bits:   bits,
			ids:    make(map[BlocksState]int),
			values: make([]BlocksState, 0, 1<<bits),
		}
	default:
		return &globalPalette[BlocksState]{}
	}
}

type biomesCfg struct{}

func (b biomesCfg) bits(bits int) int {
	switch bits {
	case 0:
		return 0
	case 1, 2, 3:
		return bits
	default:
		return biome.BitsPerBiome
	}
}

func (b biomesCfg) create(bits int) palette[BiomesState] {
	switch bits {
	case 0:
		return &singleValuePalette[BiomesState]{v: -1}
	case 1, 2, 3:
		return &linearPalette[BiomesState]{bits: bits, values: make([]BiomesState, 0, 1<<bits)}
	default:
		return &globalPalette[BiomesState]{}
	}
}

type palette[T State] interface {
	pk.Field
	// id return the index of state v in the palette and true if existed.
	// otherwise return the new bits for resize and false.
	id(v T) (int, bool)
	value(i int) T
	export() []T
}

type singleValuePalette[T State] struct {
	v T
}

func (s *singleValuePalette[T]) id(v T) (int, bool) {
	if s.v == v {
		return 0, true
	}
	// We have 2 values now. At least 1 bit is required.
	return 1, false
}

func (s *singleValuePalette[T]) value(i int) T {
	if i == 0 {
		return s.v
	}
	panic("singleValuePalette: " + strconv.Itoa(i) + " out of bounds")
}

func (s *singleValuePalette[T]) export() []T {
	return []T{s.v}
}

func (s *singleValuePalette[T]) ReadFrom(r io.Reader) (n int64, err error) {
	var i pk.VarInt
	n, err = i.ReadFrom(r)
	if err != nil {
		return
	}
	s.v = T(i)
	return
}

func (s *singleValuePalette[T]) WriteTo(w io.Writer) (n int64, err error) {
	return pk.VarInt(s.v).WriteTo(w)
}

type linearPalette[T State] struct {
	values []T
	bits   int
}

func (l *linearPalette[T]) id(v T) (int, bool) {
	for i, t := range l.values {
		if t == v {
			return i, true
		}
	}
	if cap(l.values)-len(l.values) > 0 {
		l.values = append(l.values, v)
		return len(l.values) - 1, true
	}
	return l.bits + 1, false
}

func (l *linearPalette[T]) value(i int) T {
	if i >= 0 && i < len(l.values) {
		return l.values[i]
	}
	panic("linearPalette: " + strconv.Itoa(i) + " out of bounds")
}

func (l *linearPalette[T]) export() []T {
	return l.values
}

func (l *linearPalette[T]) ReadFrom(r io.Reader) (n int64, err error) {
	var size, value pk.VarInt
	if n, err = size.ReadFrom(r); err != nil {
		return
	}
	if int(size) > cap(l.values) {
		l.values = make([]T, size)
	} else {
		l.values = l.values[:size]
	}
	for i := 0; i < int(size); i++ {
		if nn, err := value.ReadFrom(r); err != nil {
			return n + nn, err
		} else {
			n += nn
		}
		l.values[i] = T(value)
	}
	return
}

func (l *linearPalette[T]) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = pk.VarInt(len(l.values)).WriteTo(w); err != nil {
		return
	}
	for _, v := range l.values {
		if nn, err := pk.VarInt(v).WriteTo(w); err != nil {
			return n + nn, err
		} else {
			n += nn
		}
	}
	return
}

type hashPalette[T State] struct {
	ids    map[T]int
	values []T
	bits   int
}

func (h *hashPalette[T]) id(v T) (int, bool) {
	if i, ok := h.ids[v]; ok {
		return i, true
	}
	if cap(h.values)-len(h.values) > 0 {
		h.ids[v] = len(h.values)
		h.values = append(h.values, v)
		return len(h.values) - 1, true
	}
	return h.bits + 1, false
}

func (h *hashPalette[T]) value(i int) T {
	if i >= 0 && i < len(h.values) {
		return h.values[i]
	}
	panic("hashPalette: " + strconv.Itoa(i) + " out of bounds")
}

func (h *hashPalette[T]) export() []T {
	return h.values
}

func (h *hashPalette[T]) ReadFrom(r io.Reader) (n int64, err error) {
	var size, value pk.VarInt
	if n, err = size.ReadFrom(r); err != nil {
		return
	}
	if int(size) > cap(h.values) {
		h.values = make([]T, size)
	} else {
		h.values = h.values[:size]
	}
	for i := 0; i < int(size); i++ {
		if nn, err := value.ReadFrom(r); err != nil {
			return n + nn, err
		} else {
			n += nn
		}
		h.values[i] = T(value)
		h.ids[T(value)] = i
	}
	return
}

func (h *hashPalette[T]) WriteTo(w io.Writer) (n int64, err error) {
	if n, err = pk.VarInt(len(h.values)).WriteTo(w); err != nil {
		return
	}
	for _, v := range h.values {
		if nn, err := pk.VarInt(v).WriteTo(w); err != nil {
			return n + nn, err
		} else {
			n += nn
		}
	}
	return
}

type globalPalette[T State] struct{}

func (g *globalPalette[T]) id(v T) (int, bool) {
	return int(v), true
}

func (g *globalPalette[T]) value(i int) T {
	return T(i)
}

func (g *globalPalette[T]) export() []T {
	return []T{}
}

func (g *globalPalette[T]) ReadFrom(_ io.Reader) (int64, error) {
	return 0, nil
}

func (g *globalPalette[T]) WriteTo(_ io.Writer) (int64, error) {
	return 0, nil
}
