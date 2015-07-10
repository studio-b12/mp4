package mp4

import (
	"io"
)

// Sdtp Box
//
// Status: not decoded
type SdtpBox struct {
	notDecoded []byte
}

func DecodeSdtp(r io.Reader) (Box, error) {
	data, err := readAllO(r)
	if err != nil {
		return nil, err
	}
	return &SdtpBox{
		notDecoded: data[:],
	}, nil
}

func (b *SdtpBox) Type() string {
	return "sdtp"
}

func (b *SdtpBox) Size() int {
	return BoxHeaderSize + len(b.notDecoded)
}

func (b *SdtpBox) Encode(w io.Writer) error {
	err := EncodeHeader(b, w)
	if err != nil {
		return err
	}
	_, err = w.Write(b.notDecoded)
	return err
}
