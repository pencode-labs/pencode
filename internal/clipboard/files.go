package clipboard

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

func fileURIs(data string) []string {
	var files []string
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.Scheme != "file" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsRune(u.Path, 0) {
			continue
		}
		name := u.Path
		if runtime.GOOS == "windows" {
			if u.Host != "" && u.Host != "localhost" {
				name = "//" + u.Host + name
			} else if len(name) >= 3 && name[0] == '/' && name[2] == ':' {
				name = name[1:]
			}
		} else if u.Host != "" && u.Host != "localhost" {
			continue
		}
		name = filepath.FromSlash(name)
		if filepath.IsAbs(name) {
			files = append(files, name)
		}
	}
	return files
}
