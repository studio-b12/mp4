package filter

import (
	"bytes"
	"errors"
	"io"
	// "log"
	"math/rand"
	"os"
	"syscall"
	"time"

	"github.com/jfbus/mp4"
)

var (
	ErrInvalidDuration = errors.New("invalid duration")
	ErrClipOutside     = errors.New("clip zone is outside video")
	ErrTruncatedChunk  = errors.New("chunk was truncated")
)

type chunk struct {
	size      uint32
	track     int
	oldOffset uint32
}

type mdat []*chunk

func (m mdat) qsort() {
	if len(m) < 2 {
		return
	}

	left, right := 0, len(m)-1

	// Pick a pivot
	pivotIndex := rand.Int() % len(m)

	// Move the pivot to the right
	m[pivotIndex], m[right] = m[right], m[pivotIndex]

	// Pile elements smaller than the pivot on the left
	for i := range m {
		if m[i].oldOffset < m[right].oldOffset {
			m[i], m[left] = m[left], m[i]
			left++
		}
	}

	// Place the pivot after the last smaller element
	m[left], m[right] = m[right], m[left]

	// Go down the rabbit hole
	m[:left].qsort()
	m[left+1:].qsort()
}

type clipFilter struct {
	m            *mp4.MP4
	err          error
	size         int64
	chunks       mdat
	offset       int64
	buffer       []byte
	begin, end   time.Duration
	firstChunk   int
	realOffset   int64
	forskip      int64
	skipped      int64
	reader       io.Reader
	firstSample  []uint32
	lastSample   []uint32
	bufferLength int
}

type ClipInterface interface {
	Filter
	io.ReadSeeker
}

// Clip returns a filter that extracts a clip between begin and begin + duration (in seconds, starting at 0)
// Il will try to include a key frame at the beginning, and keeps the same chunks as the origin media
func Clip(m *mp4.MP4, begin, duration time.Duration) (ClipInterface, error) {
	end := begin + duration

	if begin < 0 {
		return nil, ErrClipOutside
	}

	if begin > m.Duration() {
		return nil, ErrClipOutside
	}

	if end > m.Duration() {
		end = m.Duration()
	}

	if end < 0 {
		return nil, ErrClipOutside
	}

	return &clipFilter{
		m:     m,
		end:   end,
		begin: begin,
	}, nil
}

func (f *clipFilter) Seek(offset int64, whence int) (int64, error) {
	noffset := f.offset

	if whence == os.SEEK_END {
		noffset = f.size + offset
	} else if whence == os.SEEK_SET {
		noffset = offset
	} else if whence == os.SEEK_CUR {
		noffset += offset
	} else {
		return -1, syscall.EINVAL
	}

	if noffset < 0 {
		return -1, syscall.EINVAL
	}

	if noffset > f.size {
		return -1, syscall.EINVAL
	}

	f.offset = noffset
	f.skipped = 0

	if noffset-int64(f.bufferLength) > 0 {
		f.forskip = noffset - int64(f.bufferLength)
	} else {
		f.forskip = 0
	}

	return noffset, nil
}

func (f *clipFilter) Read(buf []byte) (n int, err error) {
	var nn int

	if len(buf) == 0 {
		return
	}

	if int(f.offset) < f.bufferLength {
		nn := copy(buf, f.buffer[f.offset:])
		f.offset += int64(nn)
		n += nn
	}

	if len(buf) == n {
		return
	}

	f.buffer = nil
	s, seekable := f.reader.(io.ReadSeeker)

	for f.firstChunk < len(f.chunks) {
		c := f.chunks[f.firstChunk]

		if f.realOffset == 0 {
			f.realOffset = int64(c.oldOffset)
		}

		if f.skipped < f.forskip {
			if f.skipped+int64(c.size) > f.forskip {
				f.realOffset = int64(c.oldOffset) + (f.forskip - f.skipped)
				f.skipped += int64(c.size)
			} else {
				f.realOffset = int64(c.oldOffset + c.size)
				f.skipped += int64(c.size)
				f.firstChunk++
				continue
			}
		}

		if seekable {
			if _, err = s.Seek(f.realOffset, os.SEEK_SET); err != nil {
				return
			}
		}

		can := int(c.size - (uint32(f.realOffset) - c.oldOffset))

		if can <= 0 {
			f.firstChunk++
			continue
		}

		if can > len(buf)-n {
			can = len(buf) - n
		}

		nn, err = f.reader.Read(buf[n : n+can])
		f.offset += int64(nn)
		n += nn

		if seekable {
			f.realOffset += int64(nn)
		}

		if uint32(f.realOffset)-c.oldOffset >= c.size {
			f.firstChunk++
			f.realOffset = 0
		}

		if nn != can {
			if err == nil {
				err = ErrTruncatedChunk
			}
		}

		if err != nil {
			return
		}

		if len(buf) == n {
			return
		}
	}

	return
}

func (f *clipFilter) Filter() (err error) {
	f.buildChunkList()
	f.updateSamples()
	// co64 ?

	bsz := mp4.BoxHeaderSize
	bsz += f.m.Ftyp.Size()
	bsz += f.m.Moov.Size()

	for _, b := range f.m.Boxes() {
		bsz += b.Size()
	}

	f.chunks.qsort()

	// Update chunk offset
	var sz uint32

	iter := make([]int, len(f.m.Moov.Trak))
	stco := make([]*mp4.StcoBox, len(f.m.Moov.Trak))

	for tnum, t := range f.m.Moov.Trak {
		stco[tnum] = t.Mdia.Minf.Stbl.Stco
	}

	for _, c := range f.chunks {
		stco[c.track].ChunkOffset[iter[c.track]] = uint32(bsz) + sz
		iter[c.track]++
		sz += c.size
	}

	f.m.Mdat.ContentSize = sz

	// Prepare blob with moov and other small atoms
	buffer := make([]byte, 0)
	Buffer := bytes.NewBuffer(buffer)

	if err = f.m.Ftyp.Encode(Buffer); err != nil {
		return
	}

	if err = f.m.Moov.Encode(Buffer); err != nil {
		return
	}

	for _, b := range f.m.Boxes() {
		if err = b.Encode(Buffer); err != nil {
			return
		}
	}

	mp4.EncodeHeader(f.m.Mdat, Buffer)

	f.size = int64(f.m.Size())
	f.buffer = Buffer.Bytes()
	f.reader = f.m.Mdat.Reader()
	f.bufferLength = len(f.buffer)

	f.m = nil

	return
}

func (f *clipFilter) WriteTo(w io.Writer) (n int64, err error) {
	var nn int
	var nnn int64

	if nn, err = w.Write(f.buffer); err != nil {
		return
	}

	n += int64(nn)
	s, seekable := f.reader.(io.Seeker)

	for _, c := range f.chunks {
		csize := int64(c.size)

		if seekable {
			if _, err = s.Seek(int64(c.oldOffset), os.SEEK_SET); err != nil {
				return
			}
		}

		if nnn, err = io.CopyN(w, f.reader, csize); err != nil {
			return
		}

		if nnn != csize {
			if err == nil {
				err = ErrTruncatedChunk
			}
		}

		if err != nil {
			return
		}
	}

	return
}

func (f *clipFilter) WriteToN(dst io.Writer, size int64) (n int64, err error) {
	var nn int
	var nnn int64

	if size == 0 {
		return
	}

	for int(f.offset) < f.bufferLength && err == nil {
		nn, err = dst.Write(f.buffer[f.offset:])
		f.offset += int64(nn)
		n += int64(nn)
	}

	if size == n {
		return
	}

	f.buffer = nil
	s, seekable := f.reader.(io.ReadSeeker)

	for f.firstChunk < len(f.chunks) {
		c := f.chunks[f.firstChunk]

		if f.realOffset == 0 {
			f.realOffset = int64(c.oldOffset)
		}

		if f.skipped < f.forskip {
			if f.skipped+int64(c.size) > f.forskip {
				f.realOffset = int64(c.oldOffset) + (f.forskip - f.skipped)
				f.skipped += int64(c.size)
			} else {
				f.realOffset = int64(c.oldOffset + c.size)
				f.skipped += int64(c.size)
				f.firstChunk++
				continue
			}
		}

		if seekable {
			if _, err = s.Seek(f.realOffset, os.SEEK_SET); err != nil {
				return
			}
		}

		can := int64(c.size - (uint32(f.realOffset) - c.oldOffset))

		if can <= 0 {
			f.firstChunk++
			continue
		}

		if can > size-n {
			can = size - n
		}

		nnn, err = io.CopyN(dst, f.reader, can)
		f.offset += nnn
		n += nnn

		if seekable {
			f.realOffset += nnn
		}

		if uint32(f.realOffset)-c.oldOffset >= c.size {
			f.firstChunk++
			f.realOffset = 0
		}

		if nnn != can {
			if err == nil {
				err = ErrTruncatedChunk
			}
		}

		if err != nil {
			return
		}

		if size == n {
			return
		}
	}

	return
}

func (f *clipFilter) buildChunkList() {
	var sz int

	for _, t := range f.m.Moov.Trak {
		sz += len(t.Mdia.Minf.Stbl.Stco.ChunkOffset)
	}

	f.chunks = make([]*chunk, 0, sz)
	f.firstSample = make([]uint32, len(f.m.Moov.Trak))
	f.lastSample = make([]uint32, len(f.m.Moov.Trak))

	for tnum, t := range f.m.Moov.Trak {
		var sci, ssi int
		var firstChunk *chunk
		var start, end, p time.Duration
		var index, firstIndex, firstChunkSamples, firstChunkDescriptionID uint32

		stco := t.Mdia.Minf.Stbl.Stco
		stsz := t.Mdia.Minf.Stbl.Stsz
		stsc := t.Mdia.Minf.Stbl.Stsc
		stts := t.Mdia.Minf.Stbl.Stts
		stss := t.Mdia.Minf.Stbl.Stss
		timescale := t.Mdia.Mdhd.Timescale

		FirstChunk := []uint32{}
		SamplesPerChunk := []uint32{}
		SampleDescriptionID := []uint32{}

		if stss != nil {
			for i := 0; i < len(stss.SampleNumber); i++ {
				tc := stts.GetTimeCode(stss.SampleNumber[i]+1, timescale)

				if tc > f.begin {
					f.begin = p
					break
				}

				p = tc
			}
		}

		for i, off := range stco.ChunkOffset {
			var size uint32

			if sci < len(stsc.FirstChunk)-1 && i+1 >= int(stsc.FirstChunk[sci+1]) {
				sci++
			}

			samples := stsc.SamplesPerChunk[sci]
			descriptionID := stsc.SampleDescriptionID[sci]

			firstSample := uint32(ssi + 1)

			for i := 0; i < int(samples); i++ {
				ssi++
				size += stsz.GetSampleSize(ssi)
			}

			lastSample := uint32(ssi)

			firstTC := stts.GetTimeCode(firstSample, timescale)
			lastTC := stts.GetTimeCode(lastSample+1, timescale)

			if lastTC < f.begin || firstTC > f.end {
				continue
			}

			c := &chunk{
				size:      size,
				track:     tnum,
				oldOffset: uint32(off),
			}

			f.chunks = append(f.chunks, c)

			if f.begin >= firstTC && f.begin <= lastTC {
				if f.firstSample[tnum] == 0 {
					f.firstSample[tnum] = firstSample
				}
			}

			if f.end >= firstTC && f.end < lastTC {
				f.lastSample[tnum] = lastSample
			} else if i == len(stco.ChunkOffset)-1 {
				f.lastSample[tnum] = lastSample
			}

			if start == 0 || firstTC < start {
				start = firstTC
			}

			if end == 0 || lastTC > end {
				end = lastTC
			}

			index++

			if firstChunk == nil {
				firstChunk = c
				firstIndex = index
				firstChunkSamples = samples
				firstChunkDescriptionID = descriptionID
			}

			if samples != firstChunkSamples || descriptionID != firstChunkDescriptionID {
				FirstChunk = append(FirstChunk, firstIndex)
				SamplesPerChunk = append(SamplesPerChunk, firstChunkSamples)
				SampleDescriptionID = append(SampleDescriptionID, firstChunkDescriptionID)
				firstChunk = c
				firstIndex = index
				firstChunkSamples = samples
				firstChunkDescriptionID = descriptionID
			}
		}

		t.Tkhd.Duration = uint32((end - start) * time.Duration(f.m.Moov.Mvhd.Timescale) / time.Second)
		t.Mdia.Mdhd.Duration = uint32((end - start) * time.Duration(t.Mdia.Mdhd.Timescale) / time.Second)

		if t.Tkhd.Duration > f.m.Moov.Mvhd.Duration {
			f.m.Moov.Mvhd.Duration = t.Tkhd.Duration
		}

		if firstChunk != nil {
			FirstChunk = append(FirstChunk, firstIndex)
			SamplesPerChunk = append(SamplesPerChunk, firstChunkSamples)
			SampleDescriptionID = append(SampleDescriptionID, firstChunkDescriptionID)
		}

		t.Mdia.Minf.Stbl.Stsc.FirstChunk = FirstChunk
		t.Mdia.Minf.Stbl.Stsc.SamplesPerChunk = SamplesPerChunk
		t.Mdia.Minf.Stbl.Stsc.SampleDescriptionID = SampleDescriptionID

		// stco (chunk offsets) - build empty table to compute moov box size
		t.Mdia.Minf.Stbl.Stco.ChunkOffset = make([]uint32, index)
	}
}

func (f *clipFilter) updateSamples() {
	for tnum, t := range f.m.Moov.Trak {
		// stts - sample duration
		stts := t.Mdia.Minf.Stbl.Stts
		oldCount, oldDelta := stts.SampleCount, stts.SampleTimeDelta
		stts.SampleCount, stts.SampleTimeDelta = []uint32{}, []uint32{}

		firstSample := f.firstSample[tnum]
		lastSample := f.lastSample[tnum]

		sample := uint32(1)
		for i := 0; i < len(oldCount) && sample < lastSample; i++ {
			if sample+oldCount[i] >= firstSample {
				var current uint32
				switch {
				case sample <= firstSample && sample+oldCount[i] > lastSample:
					current = lastSample - firstSample + 1
				case sample < firstSample:
					current = oldCount[i] + sample - firstSample
				case sample+oldCount[i] > lastSample:
					current = oldCount[i] + sample - lastSample
				default:
					current = oldCount[i]
				}
				stts.SampleCount = append(stts.SampleCount, current)
				stts.SampleTimeDelta = append(stts.SampleTimeDelta, oldDelta[i])
			}
			sample += oldCount[i]
		}

		// stss (key frames)
		stss := t.Mdia.Minf.Stbl.Stss
		if stss != nil {
			oldNumber := stss.SampleNumber
			stss.SampleNumber = []uint32{}
			for _, n := range oldNumber {
				if n >= firstSample && n <= lastSample {
					stss.SampleNumber = append(stss.SampleNumber, n-uint32(firstSample)+1)
				}
			}
		}

		// stsz (sample sizes)
		stsz := t.Mdia.Minf.Stbl.Stsz
		oldSize := stsz.SampleSize
		stsz.SampleSize = []uint32{}
		for n, sz := range oldSize {
			if uint32(n) >= firstSample-1 && uint32(n) <= lastSample-1 {
				stsz.SampleSize = append(stsz.SampleSize, sz)
			}
		}

		// ctts - time offsets (b-frames)
		ctts := t.Mdia.Minf.Stbl.Ctts
		if ctts != nil {
			oldCount, oldOffset := ctts.SampleCount, ctts.SampleOffset
			ctts.SampleCount, ctts.SampleOffset = []uint32{}, []uint32{}
			sample := uint32(1)
			for i := 0; i < len(oldCount) && sample < lastSample; i++ {
				if sample+oldCount[i] >= firstSample {
					current := oldCount[i]
					if sample < firstSample && sample+oldCount[i] > firstSample {
						current += sample - firstSample
					}
					if sample+oldCount[i] > lastSample {
						current += lastSample - sample - oldCount[i]
					}

					ctts.SampleCount = append(ctts.SampleCount, current)
					ctts.SampleOffset = append(ctts.SampleOffset, oldOffset[i])
				}
				sample += oldCount[i]
			}
		}
	}
}
