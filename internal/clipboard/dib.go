package clipboard

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/png"
	"math/bits"
)

// dibPNG decodes the 24/32-bit DIB formats used by desktop screenshots.
// BI_RGB's unused fourth byte is opaque; only explicit alpha masks carry alpha.
func dibPNG(data []byte) ([]byte, error) {
	invalid := errors.New("unsupported or invalid clipboard DIB")
	if len(data) < 40 {
		return nil, invalid
	}
	u32 := binary.LittleEndian.Uint32
	header := uint64(u32(data))
	w, h := int64(int32(u32(data[4:]))), int64(int32(u32(data[8:])))
	depth := binary.LittleEndian.Uint16(data[14:])
	compression := u32(data[16:])
	if header < 40 || header > uint64(len(data)) || w <= 0 || h == 0 || binary.LittleEndian.Uint16(data[12:]) != 1 || (depth != 24 && depth != 32) || (compression != 0 && compression != 3) || (compression == 3 && depth != 32) {
		return nil, invalid
	}
	topDown := h < 0
	if topDown {
		h = -h
	}
	if w > maxImageBytes/4 || h > maxImageBytes/(w*4) {
		return nil, errImageTooLarge
	}
	stride := (uint64(w)*uint64(depth) + 31) / 32 * 4
	offset := header + uint64(u32(data[32:]))*4
	masks := [4]uint32{0xff0000, 0xff00, 0xff, 0}
	if compression == 3 {
		if header == 40 {
			offset += 12
		} else if header < 52 {
			return nil, invalid
		}
		if uint64(len(data)) < 52 {
			return nil, invalid
		}
		for i := range 3 {
			masks[i] = u32(data[40+i*4:])
		}
		if header >= 56 {
			masks[3] = u32(data[52:])
		}
		used := uint32(0)
		for i, mask := range masks {
			if mask == 0 {
				if i < 3 {
					return nil, invalid
				}
				continue
			}
			value := mask >> bits.TrailingZeros32(mask)
			if value&(value+1) != 0 || used&mask != 0 {
				return nil, invalid
			}
			used |= mask
		}
	}
	if offset > uint64(len(data)) || stride*uint64(h) > uint64(len(data))-offset {
		return nil, invalid
	}
	img := image.NewNRGBA(image.Rect(0, 0, int(w), int(h)))
	for y := range int(h) {
		sourceY := y
		if !topDown {
			sourceY = int(h) - 1 - y
		}
		row := data[offset+uint64(sourceY)*stride:]
		for x := range int(w) {
			src, dst := x*int(depth/8), y*img.Stride+x*4
			if depth == 24 {
				img.Pix[dst], img.Pix[dst+1], img.Pix[dst+2], img.Pix[dst+3] = row[src+2], row[src+1], row[src], 255
				continue
			}
			pixel := u32(row[src:])
			for c, mask := range masks {
				value := byte(255)
				if mask != 0 {
					shift := bits.TrailingZeros32(mask)
					value = byte(uint64((pixel&mask)>>shift) * 255 / uint64(mask>>shift))
				}
				img.Pix[dst+c] = value
			}
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
