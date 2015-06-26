package mp4

import (
	"io"
)

// Chpl Box (apple)
//
// Status: not decoded
type ChplBox struct {
	notDecoded []byte
}

func DecodeChpl(r io.Reader) (Box, error) {
	data, err := readAllO(r)
	if err != nil {
		return nil, err
	}
	return &ChplBox{
		notDecoded: data[:],
	}, nil
}

func (b *ChplBox) Type() string {
	return "chpl"
}

func (b *ChplBox) Size() int {
	return BoxHeaderSize + len(b.notDecoded)
}

func (b *ChplBox) Encode(w io.Writer) error {
	err := EncodeHeader(b, w)
	if err != nil {
		return err
	}
	_, err = w.Write(b.notDecoded)
	return err
}
