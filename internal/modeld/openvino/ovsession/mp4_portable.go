package ovsession

import (
	"bytes"
	"context"
	"encoding/binary"
	"math/bits"

	"github.com/contenox/runtime/internal/transport"
)

// encodePortableMP4 emits independently decodable AVC I_PCM frames. The short,
// bounded workspace clips need neither an external encoder nor a platform SDK.
func encodePortableMP4(ctx context.Context, rgb []byte, s transport.MediaSettings) ([]byte, error) {
	sps, pps := parameterSets(s.Width, s.Height)
	ftyp := mp4Box("ftyp", []byte("isom\x00\x00\x02\x00isomiso2avc1mp41"))
	var samples bytes.Buffer
	sizes := make([]uint32, s.Frames)
	stride := s.Width * s.Height * 3
	for i := range sizes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		nal := pcmFrame(rgb[i*stride:(i+1)*stride], s.Width, s.Height, i)
		sizes[i] = uint32(4 + len(nal))
		samples.Write(words(uint32(len(nal))))
		samples.Write(nal)
	}
	// All samples live in one contiguous chunk immediately after ftyp.
	chunk := uint32(len(ftyp) + 8)
	moov := movieBox(s, sps, pps, sizes, chunk)
	var file bytes.Buffer
	file.Grow(len(ftyp) + 8 + samples.Len() + len(moov))
	file.Write(ftyp)
	file.Write(words(uint32(8 + samples.Len())))
	file.WriteString("mdat")
	samples.WriteTo(&file)
	file.Write(moov)
	return file.Bytes(), nil
}

type bitstream struct {
	data []byte
	used uint
}

func (b *bitstream) write(value uint, n int) {
	for i := n - 1; i >= 0; i-- {
		if b.used == 0 {
			b.data = append(b.data, 0)
		}
		b.data[len(b.data)-1] |= byte((value>>i)&1) << (7 - b.used)
		b.used = (b.used + 1) % 8
	}
}

func (b *bitstream) ue(v uint) {
	n := bits.Len(v + 1)
	b.write(0, n-1)
	b.write(v+1, n)
}

func (b *bitstream) nal(header byte) []byte {
	b.write(1, 1) // rbsp_stop_one_bit; remaining bits in the byte are zero.
	out := make([]byte, 1, len(b.data)+1)
	out[0] = header
	zeros := 0
	for _, v := range b.data {
		if zeros == 2 && v <= 3 {
			out = append(out, 3)
			zeros = 0
		}
		out = append(out, v)
		if v == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}

func parameterSets(width, height int) ([]byte, []byte) {
	var s, p bitstream
	s.write(66, 8) // Baseline profile, constraint sets 0 and 1.
	s.write(0xc0, 8)
	s.write(51, 8) // level 5.1 covers I_PCM bitrate at the largest allowed dimensions.
	s.ue(0)        // seq_parameter_set_id
	s.ue(0)        // log2_max_frame_num_minus4
	s.ue(2)        // pic_order_cnt_type
	s.ue(1)        // max_num_ref_frames
	s.write(0, 1)  // gaps_in_frame_num_value_allowed_flag
	s.ue(uint(width/16 - 1))
	s.ue(uint(height/16 - 1))
	s.write(1, 1) // frame_mbs_only_flag
	s.write(1, 1) // direct_8x8_inference_flag
	s.write(0, 2) // no cropping, no VUI
	p.ue(0)
	p.ue(0)       // PPS and SPS indices
	p.write(0, 2) // CAVLC, no bottom-field ordering
	p.ue(0)
	p.ue(0)
	p.ue(0)       // one slice group and default reference counts
	p.write(0, 3) // no weighted prediction
	p.ue(0)
	p.ue(0)
	p.ue(0)       // signed Exp-Golomb zero for QP, QS and chroma offset
	p.write(0, 3) // no deblocking parameters, constrained intra or redundant pictures
	return s.nal(0x67), p.nal(0x68)
}

func pcmFrame(rgb []byte, width, height, index int) []byte {
	var b bitstream
	b.data = make([]byte, 0, width*height*3/2)
	b.ue(0)
	b.ue(2)
	b.ue(0)               // first macroblock, I slice, PPS index
	b.write(0, 4)         // frame_num resets at every IDR
	b.ue(uint(index % 2)) // consecutive IDRs must have different idr_pic_id
	b.write(0, 2)         // reference picture marking
	b.ue(0)               // slice_qp_delta
	var yuv [384]byte
	for top := 0; top < height; top += 16 {
		for left := 0; left < width; left += 16 {
			b.ue(25) // I_PCM macroblock, followed by alignment zero bits
			b.used = 0
			for y := 0; y < 16; y += 2 {
				for x := 0; x < 16; x += 2 {
					var sr, sg, sb int
					for dy := 0; dy < 2; dy++ {
						for dx := 0; dx < 2; dx++ {
							at := ((top+y+dy)*width + left + x + dx) * 3
							r, g, blue := int(rgb[at]), int(rgb[at+1]), int(rgb[at+2])
							yuv[(y+dy)*16+x+dx] = byte(16 + ((66*r + 129*g + 25*blue + 128) >> 8))
							sr += r
							sg += g
							sb += blue
						}
					}
					uv := (y/2)*8 + x/2
					yuv[256+uv] = byte(128 + ((-38*sr - 74*sg + 112*sb + 512) >> 10))
					yuv[320+uv] = byte(128 + ((112*sr - 94*sg - 18*sb + 512) >> 10))
				}
			}
			b.data = append(b.data, yuv[:]...)
		}
	}
	return b.nal(0x65)
}

func words(values ...uint32) []byte {
	out := make([]byte, 0, 4*len(values))
	for _, v := range values {
		out = binary.BigEndian.AppendUint32(out, v)
	}
	return out
}

func mp4Box(kind string, parts ...[]byte) []byte {
	size := 8
	for _, part := range parts {
		size += len(part)
	}
	out := make([]byte, 8, size)
	binary.BigEndian.PutUint32(out, uint32(size))
	copy(out[4:], kind)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func movieBox(s transport.MediaSettings, sps, pps []byte, sizes []uint32, chunk uint32) []byte {
	duration, scale := uint32(s.Frames), uint32(s.FPS)
	matrix := words(1<<16, 0, 0, 0, 1<<16, 0, 0, 0, 1<<30)
	mvhd := mp4Box("mvhd", words(0, 0, 0, scale, duration, 1<<16, 1<<24, 0, 0), matrix, make([]byte, 24), words(2))
	tkhd := mp4Box("tkhd", words(7, 0, 0, 1, 0, duration, 0, 0, 0, 0), matrix, words(uint32(s.Width)<<16, uint32(s.Height)<<16))
	mdhd := mp4Box("mdhd", words(0, 0, 0, scale, duration, 0x55c40000)) // undetermined language
	hdlr := mp4Box("hdlr", words(0, 0), []byte("vide"), make([]byte, 12), []byte("Video\x00"))
	dref := mp4Box("dref", words(0, 1), mp4Box("url ", words(1)))
	vmhd := mp4Box("vmhd", words(1, 0, 0))
	avcc := []byte{1, sps[1], sps[2], sps[3], 0xff, 0xe1}
	avcc = binary.BigEndian.AppendUint16(avcc, uint16(len(sps)))
	avcc = append(avcc, sps...)
	avcc = append(avcc, 1)
	avcc = binary.BigEndian.AppendUint16(avcc, uint16(len(pps)))
	avcc = append(avcc, pps...)
	entry := make([]byte, 78)
	binary.BigEndian.PutUint16(entry[6:], 1) // data reference index
	binary.BigEndian.PutUint16(entry[24:], uint16(s.Width))
	binary.BigEndian.PutUint16(entry[26:], uint16(s.Height))
	binary.BigEndian.PutUint32(entry[28:], 72<<16)
	binary.BigEndian.PutUint32(entry[32:], 72<<16)
	binary.BigEndian.PutUint16(entry[40:], 1) // frames per sample
	binary.BigEndian.PutUint16(entry[74:], 24)
	binary.BigEndian.PutUint16(entry[76:], 0xffff)
	stsd := mp4Box("stsd", words(0, 1), mp4Box("avc1", entry, mp4Box("avcC", avcc)))
	stbl := mp4Box("stbl", stsd, mp4Box("stts", words(0, 1, duration, 1)), mp4Box("stsc", words(0, 1, 1, duration, 1)),
		mp4Box("stsz", words(0, 0, duration), words(sizes...)), mp4Box("stco", words(0, 1, chunk)))
	minf := mp4Box("minf", vmhd, mp4Box("dinf", dref), stbl)
	return mp4Box("moov", mvhd, mp4Box("trak", tkhd, mp4Box("mdia", mdhd, hdlr, minf)))
}
