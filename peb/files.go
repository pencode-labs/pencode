package peb

// FileChanges comes from the host's unified file service, never a private
// extension watcher. Paths are relative to Root. Reset requests reconciliation
// after native, history or subscriber overflow; a rename may appear as remove
// and create. The batch owns its paths and may be retained by the handler.
type FileChanges struct {
	Root  string
	Files []FileEvent
	Reset bool
}
type FileEvent struct {
	Path      string
	Op        FileOperation
	Directory bool
}
type FileOperation uint8

const (
	FileCreated FileOperation = 1 << iota
	FileWritten
	FileRemoved
	FileRenamed
	FileMetadataChanged
)

func EncodeFileChanges(dst []byte, v FileChanges) ([]byte, error) {
	e := encoder{data: dst[:0]}
	e.str(v.Root)
	e.number(boolNumber(v.Reset))
	e.count(len(v.Files))
	for _, file := range v.Files {
		e.str(file.Path)
		e.number(uint32(file.Op))
		e.number(boolNumber(file.Directory))
	}
	return e.data, e.err
}
func DecodeFileChanges(data []byte) (FileChanges, error) {
	d := decoder{data: data}
	v := FileChanges{Root: d.str(), Reset: d.boolean()}
	count := d.count()
	v.Files = make([]FileEvent, count)
	for i := range v.Files {
		v.Files[i] = FileEvent{Path: d.str(), Op: FileOperation(d.number()), Directory: d.boolean()}
	}
	return v, d.done()
}
