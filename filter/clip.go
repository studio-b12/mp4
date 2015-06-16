package filter

import (
	"bytes"
	"errors"
	"io"
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

type clipFilter struct {
	m            *mp4.MP4
	err          error
	size         int64
	chunks       []chunk
	offset       int64
	buffer       []byte
	begin, end   time.Duration
	firstChunk   int
	realOffset   int64
	forskip      int64
	skipped      int64
	reader       io.Reader
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

		if int(f.offset) >= f.bufferLength {
			f.buffer = nil
		}
	}

	if len(buf) == n {
		return
	}

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

	bsz := mp4.BoxHeaderSize
	bsz += f.m.Ftyp.Size()
	bsz += f.m.Moov.Size()

	for _, b := range f.m.Boxes() {
		bsz += b.Size()
	}

	qsort(f.chunks)

	// Update chunk offset
	var sz uint32

	iter := make([]int, len(f.m.Moov.Trak), len(f.m.Moov.Trak))
	stco := make([]*mp4.StcoBox, len(f.m.Moov.Trak), len(f.m.Moov.Trak))

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

		if int(f.offset) >= f.bufferLength {
			f.buffer = nil
		}
	}

	if size == n {
		return
	}

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
	var sample, current uint32

	for _, t := range f.m.Moov.Trak {
		sz += len(t.Mdia.Minf.Stbl.Stco.ChunkOffset)
	}

	f.chunks = make([]chunk, 0, sz)
	f.m.Moov.Mvhd.Duration = 0

	var fBegin, fEnd uint32

	for tnum, t := range f.m.Moov.Trak {
		var sci, ssi int
		var firstChunk *chunk
		var start, end, p, index,
			firstIndex, tFirstSample, tLastSample,
			firstChunkSamples, firstChunkDescriptionID uint32

		stco := t.Mdia.Minf.Stbl.Stco
		stsz := t.Mdia.Minf.Stbl.Stsz
		stsc := t.Mdia.Minf.Stbl.Stsc
		stts := t.Mdia.Minf.Stbl.Stts

		FirstChunk := []uint32{}
		SamplesPerChunk := []uint32{}
		SampleDescriptionID := []uint32{}

		fBegin = uint32(int64(f.begin) * int64(t.Mdia.Mdhd.Timescale) / int64(time.Second))
		fEnd = uint32(int64(f.end) * int64(t.Mdia.Mdhd.Timescale) / int64(time.Second))

		// Find close l-frame
		if stss := t.Mdia.Minf.Stbl.Stss; stss != nil {
			for i := 0; i < len(stss.SampleNumber); i++ {
				tc := stts.GetTimeCode(stss.SampleNumber[i] - 1)

				if tc > fBegin {
					fBegin = p
					f.begin = time.Second * time.Duration(p) / time.Duration(t.Mdia.Mdhd.Timescale)
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

			firstTC := stts.GetTimeCode(firstSample)
			lastTC := stts.GetTimeCode(lastSample + 1)

			if lastTC < fBegin || firstTC > fEnd {
				continue
			}

			f.chunks = append(f.chunks, chunk{
				size:      size,
				track:     tnum,
				oldOffset: off,
			})

			if fBegin >= firstTC && fBegin <= lastTC {
				if tFirstSample == 0 {
					tFirstSample = firstSample
				}
			}

			if fEnd >= firstTC && fEnd < lastTC {
				tLastSample = lastSample
			} else if i == len(stco.ChunkOffset)-1 {
				tLastSample = lastSample
			}

			if start == 0 || firstTC < start {
				start = firstTC
			}

			if end == 0 || lastTC > end {
				end = lastTC
			}

			index++

			if firstChunk == nil {
				firstIndex = index
				firstChunk = &f.chunks[len(f.chunks)-1]
				firstChunkSamples = samples
				firstChunkDescriptionID = descriptionID
			}

			if samples != firstChunkSamples || descriptionID != firstChunkDescriptionID {
				FirstChunk = append(FirstChunk, firstIndex)
				SamplesPerChunk = append(SamplesPerChunk, firstChunkSamples)
				SampleDescriptionID = append(SampleDescriptionID, firstChunkDescriptionID)
				firstIndex = index
				firstChunk = &f.chunks[len(f.chunks)-1]
				firstChunkSamples = samples
				firstChunkDescriptionID = descriptionID
			}
		}

		t.Tkhd.Duration = ((end - start) / t.Mdia.Mdhd.Timescale) * f.m.Moov.Mvhd.Timescale
		t.Mdia.Mdhd.Duration = end - start

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
		t.Mdia.Minf.Stbl.Stco.ChunkOffset = make([]uint32, index, index)

		// stts - sample duration
		if stts := t.Mdia.Minf.Stbl.Stts; stts != nil {
			sample = 1
			current = 0

			oldCount := stts.SampleCount
			oldDelta := stts.SampleTimeDelta

			stts.SampleCount = make([]uint32, 0, len(oldCount))
			stts.SampleTimeDelta = make([]uint32, 0, len(oldDelta))

			for i := 0; i < len(oldCount) && sample < tLastSample; i++ {
				if sample+oldCount[i] >= tFirstSample {
					switch {
					case sample <= tFirstSample && sample+oldCount[i] > tLastSample:
						current = tLastSample - tFirstSample + 1
					case sample < tFirstSample:
						current = oldCount[i] + sample - tFirstSample
					case sample+oldCount[i] > tLastSample:
						current = oldCount[i] + sample - tLastSample
					default:
						current = oldCount[i]
					}

					stts.SampleCount = append(stts.SampleCount, current)
					stts.SampleTimeDelta = append(stts.SampleTimeDelta, oldDelta[i])
				}

				sample += oldCount[i]
			}
		}

		// stss (key frames)
		if stss := t.Mdia.Minf.Stbl.Stss; stss != nil {
			oldNumber := stss.SampleNumber
			stss.SampleNumber = make([]uint32, 0, len(oldNumber))

			for _, n := range oldNumber {
				if n >= tFirstSample && n <= tLastSample {
					stss.SampleNumber = append(stss.SampleNumber, n-tFirstSample+1)
				}
			}
		}

		// stsz (sample sizes)
		if stsz := t.Mdia.Minf.Stbl.Stsz; stsz != nil {
			oldSize := stsz.SampleSize
			stsz.SampleSize = make([]uint32, 0, len(oldSize))

			for n, sz := range oldSize {
				if uint32(n) >= tFirstSample-1 && uint32(n) <= tLastSample-1 {
					stsz.SampleSize = append(stsz.SampleSize, sz)
				}
			}
		}

		// ctts - time offsets (b-frames)
		if ctts := t.Mdia.Minf.Stbl.Ctts; ctts != nil {
			sample = 1

			oldCount := ctts.SampleCount
			oldOffset := ctts.SampleOffset

			ctts.SampleCount = make([]uint32, 0, len(oldCount))
			ctts.SampleOffset = make([]uint32, 0, len(oldOffset))

			for i := 0; i < len(oldCount) && sample < tLastSample; i++ {
				if sample+oldCount[i] >= tFirstSample {
					current := oldCount[i]

					if sample+oldCount[i] > tFirstSample && sample < tFirstSample {
						current += sample - tFirstSample
					}

					if sample+oldCount[i] > tLastSample {
						current += tLastSample - sample - oldCount[i]
					}

					ctts.SampleCount = append(ctts.SampleCount, current)
					ctts.SampleOffset = append(ctts.SampleOffset, oldOffset[i])
				}

				sample += oldCount[i]
			}
		}

		// co64 ?
	}
}

func qsort(m []chunk) {
Start:
	if len(m) <= 3 {
		if len(m) >= 2 {
			if m[0].oldOffset > m[1].oldOffset {
				m[0], m[1] = m[1], m[0]
			}

			if len(m) == 3 {
				if m[1].oldOffset > m[2].oldOffset {
					m[1], m[2] = m[2], m[1]

					if m[0].oldOffset > m[1].oldOffset {
						m[0], m[1] = m[1], m[0]
					}
				}
			}
		}

		return
	}

	left, right := 0, len(m)-1
	pivotOffset := m[0].oldOffset/4 + m[len(m)/4].oldOffset/4 + m[len(m)*3/4-1].oldOffset/4 + m[len(m)-1].oldOffset/4

	for m[left].oldOffset < pivotOffset {
		left++
	}

	for m[right].oldOffset >= pivotOffset {
		right--
	}

	for i := left + 1; i <= right; i++ {
		if m[i].oldOffset < pivotOffset {
			m[i], m[left] = m[left], m[i]
			left++
		}
	}

	// Go down the rabbit hole
	qsort(m[:left])

	m = m[left:]
	goto Start
}
