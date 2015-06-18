package mp4

import "io"

// User Data Box (udta - optional)
//
// Contained in: Movie Box (moov) or Track Box (trak)
type UdtaBox struct {
	Meta *MetaBox
	Name *NameBox
}

func DecodeUdta(r io.Reader) (Box, error) {
	l, err := DecodeContainer(r)
	if err != nil {
		return nil, err
	}
	u := &UdtaBox{}
	for _, b := range l {
		switch b.Type() {
		case "meta":
			u.Meta = b.(*MetaBox)
		case "name":
			u.Name = b.(*NameBox)
		default:
			return nil, ErrBadFormat
		}
	}
	return u, nil
}

func (b *UdtaBox) Type() string {
	return "udta"
}

func (b *UdtaBox) Size() int {
	sz := BoxHeaderSize
	if b.Meta != nil {
		sz += b.Meta.Size()
	}
	if b.Name != nil {
		sz += b.Name.Size()
	}
	return sz
}

func (b *UdtaBox) Encode(w io.Writer) error {
	err := EncodeHeader(b, w)
	if err != nil {
		return err
	}
	if b.Meta != nil {
		err = b.Meta.Encode(w)
		if err != nil {
			return err
		}

	}
	if b.Name != nil {
		err = b.Name.Encode(w)
		if err != nil {
			return err
		}

	}
	return nil
}
