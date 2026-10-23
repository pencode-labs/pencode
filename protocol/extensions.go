package protocol

// ExtensionManifest is installed on the server at resources/extension.json.
type ExtensionManifest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Version     string   `json:"version,omitempty"`
	Publisher   string   `json:"publisher,omitempty"`
	Description string   `json:"description,omitempty"`
	Entry       string   `json:"entry"`
	Args        []string `json:"args,omitempty"`
	Disabled    bool     `json:"disabled,omitempty"`
}

type ExtensionInfo struct {
	ExtensionManifest
	Path, Problem string
}

func (i *ExtensionInfo) Label() string {
	if i.Name != "" {
		return i.Name
	}
	return i.ID
}

func EncodeExtension(e *Encoder, i ExtensionInfo) {
	e.String(i.ID)
	e.String(i.Name)
	e.String(i.Version)
	e.String(i.Publisher)
	e.String(i.Description)
	e.String(i.Entry)
	e.U32(uint32(len(i.Args)))
	for _, a := range i.Args {
		e.String(a)
	}
	if i.Disabled {
		e.U32(1)
	} else {
		e.U32(0)
	}
	e.String(i.Path)
	e.String(i.Problem)
}

func DecodeExtension(d *Decoder) ExtensionInfo {
	i := ExtensionInfo{ExtensionManifest: ExtensionManifest{
		ID: d.String(), Name: d.String(), Version: d.String(), Publisher: d.String(), Description: d.String(), Entry: d.String(),
	}}
	n := d.U32()
	if n > 4096 {
		d.Err = ErrProtocol
		return i
	}
	i.Args = make([]string, n)
	for j := range i.Args {
		i.Args[j] = d.String()
	}
	b := d.U32()
	if b > 1 {
		d.Err = ErrProtocol
	}
	i.Disabled = b == 1
	i.Path, i.Problem = d.String(), d.String()
	return i
}
