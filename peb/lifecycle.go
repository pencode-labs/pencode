package peb

// LifecycleKind identifies a host close request or a completed project closure.
type LifecycleKind uint32

const (
	WindowClosing LifecycleKind = iota
	ProjectClosing
	ProjectClosed
)

// LifecycleEvent reaches the action process even when all its views are closed.
// ProjectPath is absolute for project events and empty for WindowClosing.
type LifecycleEvent struct {
	Kind        LifecycleKind
	ProjectPath string
}

// LifecycleReply asks the host to confirm closure before proceeding. Empty
// Message allows closure. ProjectClosed replies cannot prevent closure.
type LifecycleReply struct{ Message string }

func EncodeLifecycleEvent(dst []byte, v LifecycleEvent) ([]byte, error) {
	e := encoder{data: dst[:0]}
	if v.Kind > ProjectClosed {
		return e.data, ErrProtocol
	}
	e.number(uint32(v.Kind))
	e.str(v.ProjectPath)
	return e.data, e.err
}

func DecodeLifecycleEvent(data []byte) (LifecycleEvent, error) {
	d := decoder{data: data}
	v := LifecycleEvent{Kind: LifecycleKind(d.number()), ProjectPath: d.str()}
	if v.Kind > ProjectClosed {
		d.err = ErrProtocol
	}
	return v, d.done()
}

func EncodeLifecycleReply(dst []byte, v LifecycleReply) ([]byte, error) {
	e := encoder{data: dst[:0]}
	e.str(v.Message)
	return e.data, e.err
}

func DecodeLifecycleReply(data []byte) (LifecycleReply, error) {
	d := decoder{data: data}
	v := LifecycleReply{Message: d.str()}
	return v, d.done()
}
