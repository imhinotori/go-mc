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

func (m Message) TagType() byte {
	return nbt.TagCompound
}

func (m Message) MarshalNBT(w io.Writer) error {
	// The nbt.Marshaler contract is to write only the tag BODY: the outer encoder
	// has already written the compound tag byte (see Message.TagType). Encoding a
	// full document here would double the tag header (e.g. 0A 0A 00 00 ...), which
	// the network-format decoder then fails to parse. We therefore encode the chosen
	// struct in network format (which prefixes a single tag byte and no name) and
	// strip that one leading tag byte so only the compound body reaches w.
	var v any
	if m.Translate != "" {
		v = translateMsg(m)
	} else {
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
