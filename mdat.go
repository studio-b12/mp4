package mp4

import "io"

// Media Data Box (mdat - optional)
//
// Status: not decoded
//
// The mdat box contains media chunks/samples.
//
// It is not read, only the io.ReadSeeker is stored, and will be used to Encode (io.Copy) the box to a io.Writer.
type MdatBox struct {
	ContentSize uint64
	Offset      uint64 // offset of the begging of the file
	r           io.ReadSeeker
}

func DecodeMdat(r io.ReadSeeker, size uint64) (Box, error) {
	return &MdatBox{r: r, ContentSize: size}, nil
}

func (b *MdatBox) Type() string {
	return "mdat"
}

func (b *MdatBox) Size() uint64 {
	return b.ContentSize
}

func (b *MdatBox) ReadSeeker() io.ReadSeeker {
	b.r.Seek(int64(b.Offset), 0)
	return b.r
}

func (b *MdatBox) Encode(w io.Writer) error {
	err := EncodeHeader(b, w)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, b.r)
	return err
}
