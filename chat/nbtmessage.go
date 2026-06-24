package chat

import (
	"bytes"
	"errors"
	"io"
	"strconv"

	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// ReadFrom decode Message in a Text component
func (m *Message) ReadFrom(r io.Reader) (int64, error) {
	return pk.NBT(m).ReadFrom(r)
}

// WriteTo encode Message into a Text component
func (m Message) WriteTo(w io.Writer) (int64, error) {
	return pk.NBT(&m).WriteTo(w)
}

// isPlainText reports whether the message carries ONLY a Text body — every other
// component field (styling, events, translate, extra) is zero. Vanilla 26.2 serializes
// such a component as a bare NBT TAG_String (the shorthand a Component StreamCodec emits
// for a literal text component), NOT as a {"text":...} TAG_Compound. Capturing a real
// vanilla ClientboundSystemChat confirmed this: vanilla writes `08 <len> <utf8>` (TAG_String)
// where the compound form would write `0a 08 0004 74657874 …` (TAG_Compound). Encoding the
// compound form is accepted by the client (a string is shorthand for a text component) but
// DIVERGES from the vanilla wire — this predicate selects the faithful string form (07-06
// capture-diff). A message with any styling/event/translate/extra still needs the compound.
func (m Message) isPlainText() bool {
	return m.Text != "" &&
		!m.Bold && !m.Italic && !m.UnderLined && !m.StrikeThrough && !m.Obfuscated &&
		m.Font == "" && m.Color == "" && m.Insertion == "" &&
		m.ClickEvent == nil && m.HoverEvent == nil &&
		m.Translate == "" && len(m.With) == 0 && len(m.Extra) == 0
}

func (m Message) TagType() byte {
	// A text-only component serializes as a bare TAG_String to match the vanilla 26.2 wire
	// (confirmed by the 07-06 ClientboundSystemChat capture-diff). Anything richer is a
	// TAG_Compound.
	if m.isPlainText() {
		return nbt.TagString
	}
	return nbt.TagCompound
}

func (m Message) MarshalNBT(w io.Writer) error {
	// The nbt.Marshaler contract is to write only the tag BODY: the outer encoder has
	// already written the tag byte (see Message.TagType). Encoding a full document here would
	// double the tag header (e.g. 0A 0A 00 00 ...), which the network-format decoder then
	// fails to parse. We therefore encode the chosen Go value in network format (which
	// prefixes a single tag byte and no name) and strip that one leading tag byte so only the
	// body reaches w.

	// VANILLA-FAITHFUL FAST PATH: a text-only component is a bare TAG_String body
	// (length-prefixed UTF-8), matching the vanilla SystemChat wire (07-06 capture-diff).
	// TagType() already announced TagString, so we must write a STRING body here, not a
	// compound — otherwise the tag byte (08) and the body (a compound) would disagree and the
	// client mis-frames the packet.
	var v any
	switch {
	case m.isPlainText():
		v = m.Text
	case m.Translate != "":
		v = translateMsg(m)
	default:
		v = rawMsgStruct(m)
	}

	var buf bytes.Buffer
	enc := nbt.NewEncoder(&buf)
	enc.NetworkFormat(true) // writes [tagByte][body...], no name
	if err := enc.Encode(v, ""); err != nil {
		return err
	}
	body := buf.Bytes()
	if len(body) == 0 {
		return errors.New("chat: empty nbt encoding for message")
	}
	_, err := w.Write(body[1:]) // drop the duplicate leading tag byte; keep the body
	return err
}

func (m *Message) UnmarshalNBT(tagType byte, r nbt.DecoderReader) error {
	// Re-combine the tagType into the reader, and create a nbt decoder
	tagReader := bytes.NewReader([]byte{tagType})
	decoder := nbt.NewDecoder(io.MultiReader(tagReader, r))
	decoder.NetworkFormat(true) // TagType directlly followed the body

	switch tagType {
	case nbt.TagString:
		_, err := decoder.Decode(&m.Text)
		return err
	case nbt.TagCompound:
		_, err := decoder.Decode((*rawMsgStruct)(m))
		return err
	case nbt.TagList:
		_, err := decoder.Decode(&m.Extra)
		return err
	default:
		return errors.New("unknown chat message type: '" + strconv.FormatUint(uint64(tagType), 16) + "'")
	}
}

func (t *TranslateArgs) UnmarshalNBT(tagType byte, r nbt.DecoderReader) error {
	tagReader := bytes.NewReader([]byte{tagType})
	decoder := nbt.NewDecoder(io.MultiReader(tagReader, r))
	decoder.NetworkFormat(true) // TagType directlly followed the body

	switch tagType {
	case nbt.TagList:
		var value []Message
		if _, err := decoder.Decode(&value); err != nil {
			return err
		}
		for _, v := range value {
			*t = append(*t, v)
		}
		return nil
	case nbt.TagByteArray:
		var value []int8
		if _, err := decoder.Decode(&value); err != nil {
			return err
		}
		for _, v := range value {
			*t = append(*t, strconv.FormatInt(int64(v), 10))
		}
		return nil
	case nbt.TagIntArray:
		var value []int32
		if _, err := decoder.Decode(&value); err != nil {
			return err
		}
		for _, v := range value {
			*t = append(*t, strconv.FormatInt(int64(v), 10))
		}
		return nil
	case nbt.TagLongArray:
		var value []int64
		if _, err := decoder.Decode(&value); err != nil {
			return err
		}
		for _, v := range value {
			*t = append(*t, strconv.FormatInt(int64(v), 10))
		}
		return nil
	default:
		return errors.New("unknown translation args type: '" + strconv.FormatUint(uint64(tagType), 16) + "'")
	}
}
