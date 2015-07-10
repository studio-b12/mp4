package mp4

import (
	"io"
)

// Sbgp Box
//
// Status: not decoded
type SbgpBox struct {
	notDecoded []byte
}

func DecodeSbgp(r io.Reader) (Box, error) {
	data, err := readAllO(r)
	if err != nil {
		return nil, err
	}
	return &SbgpBox{
		notDecoded: data[:],
	}, nil
}

func (b *SbgpBox) Type() string {
	return "sbgp"
}

func (b *SbgpBox) Size() int {
	return BoxHeaderSize + len(b.notDecoded)
}

func (b *SbgpBox) Encode(w io.Writer) error {
	err := EncodeHeader(b, w)
	if err != nil {
		return err
	}
	_, err = w.Write(b.notDecoded)
	return err
}
