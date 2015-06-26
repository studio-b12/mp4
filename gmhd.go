package mp4

import (
	"io"
)

// Gmhd Box (apple)
//
// Status: not decoded
type GmhdBox struct {
	notDecoded []byte
}

func DecodeGmhd(r io.Reader) (Box, error) {
	data, err := readAllO(r)
	if err != nil {
		return nil, err
	}
	return &GmhdBox{
		notDecoded: data[:],
	}, nil
}

func (b *GmhdBox) Type() string {
	return "gmhd"
}

func (b *GmhdBox) Size() int {
	return BoxHeaderSize + len(b.notDecoded)
}

func (b *GmhdBox) Encode(w io.Writer) error {
	err := EncodeHeader(b, w)
	if err != nil {
		return err
	}
	_, err = w.Write(b.notDecoded)
	return err
}
