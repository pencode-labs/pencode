package clipboard

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"testing"
)

func testDIB(header, depth int, height int32, pixels []byte) []byte {
	data := make([]byte, header+len(pixels))
	binary.LittleEndian.PutUint32(data, uint32(header))
	binary.LittleEndian.PutUint32(data[4:], 1)
	binary.LittleEndian.PutUint32(data[8:], uint32(height))
	binary.LittleEndian.PutUint16(data[12:], 1)
	binary.LittleEndian.PutUint16(data[14:], uint16(depth))
	copy(data[header:], pixels)
	return data
}

func TestDIBPNG(t *testing.T) {
	for _, depth := range []int{24, 32} {
		for _, height := range []int32{2, -2} {
			// Each row occupies four bytes, including the padding for 24-bit data.
			data := testDIB(40, depth, height, []byte{0, 0, 255, 0, 255, 0, 0, 0})
			encoded, err := dibPNG(data)
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(encoded))
			if err != nil {
				t.Fatal(err)
			}
			first, second := color.NRGBA{R: 255, A: 255}, color.NRGBA{B: 255, A: 255}
			if height > 0 {
				first, second = second, first
			}
			if got := color.NRGBAModel.Convert(img.At(0, 0)); got != first {
				t.Fatalf("depth=%d height=%d: first pixel %v", depth, height, got)
			}
			if got := color.NRGBAModel.Convert(img.At(0, 1)); got != second {
				t.Fatalf("second pixel %v", got)
			}
		}
	}
}

func TestDIBBitfieldsAlpha(t *testing.T) {
	for _, header := range []int{52, 56, 108, 124} {
		data := testDIB(header, 32, -1, []byte{20, 40, 60, 128})
		binary.LittleEndian.PutUint32(data[16:], 3)
		for i, mask := range []uint32{0xff0000, 0xff00, 0xff} {
			binary.LittleEndian.PutUint32(data[40+i*4:], mask)
		}
		alpha := uint8(255)
		if header >= 56 {
			binary.LittleEndian.PutUint32(data[52:], 0xff000000)
			alpha = 128
		}
		encoded, err := dibPNG(data)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		if got := color.NRGBAModel.Convert(img.At(0, 0)); got != (color.NRGBA{R: 60, G: 40, B: 20, A: alpha}) {
			t.Fatalf("header=%d: pixel %v", header, got)
		}
	}
}

func TestDIBRejectsInvalidBuffers(t *testing.T) {
	valid := testDIB(40, 32, 1, []byte{0, 0, 0, 0})
	for n := range len(valid) {
		if _, err := dibPNG(valid[:n]); err == nil {
			t.Fatalf("accepted truncated DIB of %d bytes", n)
		}
	}
	for _, offset := range []int{0, 4, 8, 12, 14, 16, 32} {
		data := bytes.Clone(valid)
		binary.LittleEndian.PutUint32(data[offset:], 0xffffffff)
		if offset == 8 {
			binary.LittleEndian.PutUint32(data[offset:], 0x7fffffff)
		}
		if _, err := dibPNG(data); err == nil {
			t.Fatalf("accepted malformed field at %d", offset)
		}
	}
}

func FuzzDIBPNG(f *testing.F) {
	f.Add(testDIB(40, 32, 1, []byte{0, 0, 0, 0}))
	f.Fuzz(func(t *testing.T, data []byte) {
		// Exercise validation and pixel conversion without large fuzz allocations.
		if len(data) >= 12 && (binary.LittleEndian.Uint32(data[4:]) > 64 || binary.LittleEndian.Uint32(data[8:]) > 64) {
			return
		}
		_, _ = dibPNG(data)
	})
}
