package mp4

import (
	"io"
	"io/ioutil"
)

// Name Box
//
// Status: not decoded
type NameBox struct {
	notDecoded []byte
}

func DecodeName(r io.Reader) (Box, error) {
	data, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return &NameBox{
		notDecoded: data[:],
	}, nil
}

func (b *NameBox) Type() string {
	return "name"
}

func (b *NameBox) Size() int {
	return BoxHeaderSize + len(b.notDecoded)
}

func (b *NameBox) Encode(w io.Writer) error {
	err := EncodeHeader(b, w)
	if err != nil {
		return err
	}
	buf := makebuf(b)
	copy(buf[:], b.notDecoded)
	_, err = w.Write(buf)
	return err
}
