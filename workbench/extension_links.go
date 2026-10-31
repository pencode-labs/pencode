package workbench

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/pencode-labs/pencode/peb"
)

func extensionOwnsClick(bindings []peb.EditorBinding, ref string) bool {
	for _, binding := range bindings {
		if binding.Ref == ref && binding.Kind == "click" {
			return true
		}
	}
	return false
}

// Host-owned anchors keep navigation out of the extension event queue. Their
// lookup table is rebuilt only when a view changes, including region patches.
func (s *pencodeExtensionSurface) openLink(href string) {
	u, err := url.Parse(href)
	if err != nil {
		if colon := strings.LastIndexByte(href, ':'); colon >= 0 {
			if _, numberErr := strconv.Atoi(href[colon+1:]); numberErr == nil {
				u, err = url.Parse("./" + href)
			}
		}
	}
	if err != nil {
		return
	}
	if u.Opaque != "" {
		if _, err := strconv.Atoi(strings.ReplaceAll(u.Opaque, ":", "")); err == nil {
			u.Path, u.Scheme, u.Opaque = "./"+u.Scheme+":"+u.Opaque, "", ""
		}
	}
	if u.Scheme == "" && u.Host != "" {
		u.Scheme = "https"
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
		channel := s.model.outputStore().Channel("Extension: " + s.owner)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := openExternalCommand(ctx, u.String()).Run(); err != nil {
				fmt.Fprintf(channel, "无法打开链接：%v\n", err)
			}
		}()
	case "", "file":
		if u.Path == "" || u.Host != "" && u.Host != "localhost" {
			return
		}
		name, line := extensionLinkLocation(u)
		root := s.invocation.context.ProjectPath
		if root == "" {
			root = s.model.projectPath
		}
		if !path.IsAbs(name) {
			name = path.Join(root, name)
		}
		name = path.Clean(name)
		var project *pencodeProject
		for _, candidate := range s.model.projects {
			if (name == candidate.Path || strings.HasPrefix(name, strings.TrimSuffix(candidate.Path, "/")+"/")) && (project == nil || len(candidate.Path) > len(project.Path)) {
				project = candidate
			}
		}
		if project == nil {
			s.model.setActiveStatus("链接不在已打开的项目中：" + name)
			return
		}
		relative := strings.TrimPrefix(name, strings.TrimSuffix(project.Path, "/")+"/")
		if !s.model.canOpenFile(relative) {
			return
		}
		s.model.activateProject(project)
		if err := s.model.requestFileOpen(project, relative, false, true, &fileNavigation{line: max(1, line)}); err != nil {
			s.model.setProjectStatus(project, err.Error())
		}
		s.model.syncActiveProjectFields()
	}
}

func extensionLinkLocation(u *url.URL) (string, int) {
	name, line := pencodeQuickLocation(u.Path)
	if filename, previous := pencodeQuickLocation(name); previous > 0 {
		name, line = filename, previous // path:line:column
	}
	fragment := strings.TrimPrefix(u.Fragment, "L")
	fragment, _, _ = strings.Cut(fragment, "-")
	fragment, _, _ = strings.Cut(fragment, "C")
	if n, err := strconv.Atoi(fragment); err == nil && n > 0 {
		line = n
	}
	return path.Clean(name), line
}
