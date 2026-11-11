package protocol

// TextPageSize bounds text sent to and laid out by the editor. Larger files
// retain a disk-backed document and expose one page at a time.
const TextPageSize = 64 << 10
const TextPagingThreshold = 4 << 20

type DocumentPage struct {
	Offset, Size uint64
	Paged        bool
}

func (e *Encoder) DocumentPage(p DocumentPage) {
	e.U64(p.Offset)
	e.U64(p.Size)
	if p.Paged {
		e.U32(1)
	} else {
		e.U32(0)
	}
}
func (d *Decoder) DocumentPage() DocumentPage {
	return DocumentPage{Offset: d.U64(), Size: d.U64(), Paged: d.U32() != 0}
}
