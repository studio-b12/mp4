package mp4

import (
	"io"
)

// Tref Box (apple)
//
// Status: not decoded
type TrefBox struct {
	notDecoded []byte
}

func DecodeTref(r io.Reader) (Box, error) {
	data, err := readAllO(r)
	if err != nil {
		return nil, err
	}
	return &TrefBox{
		notDecoded: data[:],
	}, nil
}

func (b *TrefBox) Type() string {
	return "tref"
}

func (b *TrefBox) Size() int {
	return BoxHeaderSize + len(b.notDecoded)
}

func (b *TrefBox) Encode(w io.Writer) error {
	err := EncodeHeader(b, w)
	if err != nil {
		return err
	}
	_, err = w.Write(b.notDecoded)
	return err
}
