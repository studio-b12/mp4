package filter

import (
	"io"

	"github.com/MStoykov/mp4"
)

type noopFilter struct {
	m *mp4.MP4
}

// Noop returns a filter that does nothing
func Noop(m *mp4.MP4) Filter {
	return &noopFilter{m}
}

func (f *noopFilter) Filter() (err error) {
	return
}

func (f *noopFilter) WriteTo(w io.Writer) (n int64, err error) {
	var nn int64

	if err = f.m.Ftyp.Encode(w); err != nil {
		return
	} else {
		n += int64(f.m.Ftyp.Size())
	}

	if err = f.m.Moov.Encode(w); err != nil {
		return
	} else {
		n += int64(f.m.Moov.Size())
	}

	for _, b := range f.m.Boxes() {
		if err = b.Encode(w); err != nil {
			return
		} else {
			n += int64(b.Size())
		}
	}

	if err = mp4.EncodeHeader(f.m.Mdat, w); err != nil {
		return
	}

	n += mp4.BoxHeaderSize
	nn, err = io.Copy(w, f.m.Mdat.Reader())
	n += nn

	return
}
