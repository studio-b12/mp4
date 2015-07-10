package mp4

import (
	"io"
)

// Sgpd Box
//
// Status: not decoded
type SgpdBox struct {
	notDecoded []byte
}

func DecodeSgpd(r io.Reader) (Box, error) {
	data, err := readAllO(r)
	if err != nil {
		return nil, err
	}
	return &SgpdBox{
		notDecoded: data[:],
	}, nil
}

func (b *SgpdBox) Type() string {
	return "sgpd"
}

func (b *SgpdBox) Size() int {
	return BoxHeaderSize + len(b.notDecoded)
}

func (b *SgpdBox) Encode(w io.Writer) error {
	err := EncodeHeader(b, w)
	if err != nil {
		return err
	}
	_, err = w.Write(b.notDecoded)
	return err
}
