package peb

// Notification is a host-rendered, nonmodal message in the bottom-right corner.
// ID replaces an existing message from this extension. Action is a local action
// ID invoked by its optional button; it need not contribute a workbench button.
// Close removes the message. Notifications never take focus when published.
type Notification struct {
	ID, Title, Message, Action, ActionText string
	Error, Close                           bool
}

func (n Notification) valid() bool {
	return n.ID != "" && (n.Close || n.Title != "" && n.Message != "" && (n.Action == "") == (n.ActionText == ""))
}

func EncodeNotification(dst []byte, n Notification) ([]byte, error) {
	e := encoder{data: dst[:0]}
	if !n.valid() {
		e.err = ErrProtocol
	}
	e.str(n.ID)
	e.str(n.Title)
	e.str(n.Message)
	e.str(n.Action)
	e.str(n.ActionText)
	e.number(boolNumber(n.Error))
	e.number(boolNumber(n.Close))
	return e.data, e.err
}

func DecodeNotification(data []byte) (Notification, error) {
	d := decoder{data: data}
	n := Notification{ID: d.str(), Title: d.str(), Message: d.str(), Action: d.str(), ActionText: d.str(), Error: d.boolean(), Close: d.boolean()}
	if !n.valid() {
		d.err = ErrProtocol
	}
	return n, d.done()
}

// Notify publishes a message from an action handler or background worker.
func (p *ActionPublisher) Notify(n Notification) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ctx.Err(); err != nil {
		return err
	}
	var err error
	p.data, err = EncodeNotification(p.data, n)
	if err != nil {
		return err
	}
	return p.writer.Write(NotificationUpdate, 0, p.data)
}
