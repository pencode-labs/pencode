package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"sync/atomic"
	"time"

	"github.com/onix-engine/onix"
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/project"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/internal/desktopopen"
	"github.com/pencode-labs/pencode/internal/profiling"
	"github.com/pencode-labs/pencode/internal/windowstate"
	"github.com/pencode-labs/pencode/remote"
	"github.com/pencode-labs/pencode/transport"
	"github.com/pencode-labs/pencode/workbench"
)

type pencodeWindowRequest struct {
	Paths           []string
	RestoreID       string
	ServerDirectory string
	Profile         connection.Profile
	Restore         bool
	NewWindow       bool
}

type pencodeEditorWindow struct {
	app     *onix.App
	session *workbench.Session
}

func (w *pencodeEditorWindow) focus() {
	desktopopen.Focus(w.app.Context)
	w.app.RequestRender()
}

type pencodeApplication struct {
	source                      fs.FS
	inbox                       *windowstate.LaunchInbox[pencodeWindowRequest]
	wakeWindow                  atomic.Pointer[onix.App]
	plan                        *project.ProjectPlan
	recorder                    *profiling.Recorder
	windows                     []*pencodeEditorWindow
	pending                     []pencodeWindowRequest
	executable, serverDirectory string
	native                      onix.Application
}

func (a *pencodeApplication) queueWindow(paths []string, profile connection.Profile) error {
	a.pending = append(a.pending, pencodeWindowRequest{Paths: slices.Clone(paths), Profile: profile})
	return nil
}

func (a *pencodeApplication) activateProject(from *workbench.Session, profile connection.Profile, path string) bool {
	for _, w := range a.windows {
		if w.session != from && w.session.ActivateProject(profile, path) {
			w.focus()
			return true
		}
	}
	if profile.Remote() {
		return false
	}
	var owner *pencodeEditorWindow
	var depth int
	for _, w := range a.windows {
		if root := w.session.LocalProjectRoot(path); len(root) > depth {
			owner, depth = w, len(root)
		}
	}
	if owner != nil && owner.session != from {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			owner.session.OpenPaths(path)
			owner.focus()
			return true
		}
	}
	return false
}

func (a *pencodeApplication) openPath(path string) {
	path = workbench.LocalOpenPath(path)
	if a.activateProject(nil, connection.Profile{}, path) {
		return
	}
	for _, w := range a.windows {
		if w.app.Context.IsFocused() {
			w.focus()
			w.session.OpenPaths(path)
			return
		}
	}
	if len(a.windows) != 0 {
		w := a.windows[len(a.windows)-1]
		w.focus()
		w.session.OpenPaths(path)
	} else {
		_ = a.queueWindow([]string{path}, connection.Profile{})
	}
}

func (a *pencodeApplication) routeRemote(request pencodeWindowRequest) bool {
	if !request.Profile.Remote() || request.Profile.Directory == "" {
		return false
	}
	if a.activateProject(nil, request.Profile, request.Profile.Directory) {
		return true
	}
	if !request.NewWindow {
		for _, w := range a.windows {
			if w.session.QueueRemoteProject(request.Profile) {
				w.focus()
				return true
			}
		}
	}
	return false
}

func (a *pencodeApplication) openPending() error {
	if a.inbox != nil {
		err := a.inbox.Drain(len(a.windows) == 0 && len(a.pending) == 0, func(request pencodeWindowRequest) {
			request.Restore = false
			a.pending = append(a.pending, request)
		})
		if err != nil {
			return err
		}
	}
	for len(a.pending) != 0 {
		request := a.pending[0]
		a.pending[0] = pencodeWindowRequest{}
		a.pending = a.pending[1:]
		if a.routeRemote(request) {
			continue
		}
		if len(request.Paths) != 0 {
			paths := request.Paths[:0]
			for _, path := range request.Paths {
				path = workbench.LocalOpenPath(path)
				if !a.activateProject(nil, connection.Profile{}, path) {
					paths = append(paths, path)
				}
			}
			if len(paths) == 0 {
				continue
			}
			request.Paths = paths
		}
		if _, err := a.open(request); err != nil {
			fmt.Fprintln(os.Stderr, "新建窗口失败：", err)
		}
	}
	a.pending = nil
	return nil
}

func (a *pencodeApplication) open(request pencodeWindowRequest) (others []string, err error) {
	conn, err := transport.Start(a.executable, "--pencode-server")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	backend, err := client.New(ctx, conn)
	cancel()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	session, app := workbench.NewSession(backend), onix.NewApp()
	defer func() {
		if err != nil {
			app.Destroy()
			session.Close()
		}
	}()
	others, err = session.ConfigureWindows(request.RestoreID, request.Restore)
	if err != nil {
		return nil, err
	}
	session.SetWindowOpener(a.queueWindow)
	session.SetProjectActivator(a.activateProject)
	session.OpenPaths(request.Paths...)
	dialer := connection.DefaultDialer(a.executable, session.Output.Channel(remote.OutputChannelName))
	if request.ServerDirectory != "" {
		dialer.SSH.ServerDirectory = request.ServerDirectory
	} else if a.serverDirectory != "" {
		dialer.SSH.ServerDirectory = a.serverDirectory
	}
	session.ConfigureConnection(dialer.Dial, request.Profile)
	registry, err := OnixRegistry()
	if err != nil {
		return nil, err
	}
	if err := session.Register(&registry); err != nil {
		return nil, err
	}
	if width, height := session.RestoredWindowSize(); width > 0 && height > 0 {
		app.SetInitialWindowSize(width, height)
	}
	app.AddInputHandler(input.PhaseGlobal, session.HandleFileDrop)
	app.SetRenderMode(onix.RenderOnDemand)
	app.SetIdleEnabled(true)
	if err := OnixInstallSystems(app); err != nil {
		return nil, err
	}
	if err := app.AddSystems(onix.StagePostUpdate, session.ScrollSystem(), session.LayoutSystem()); err != nil {
		return nil, err
	}
	if a.recorder != nil {
		if err := app.AddSystems(onix.StageRender, session.ProfilingSystem(a.recorder)); err != nil {
			return nil, err
		}
	}
	if err := OnixOpenWindow(app, a.plan.Spec, a.source); err != nil {
		return nil, err
	}
	if _, err := app.LoadModules[OnixContentProfile](a.plan, registry, OnixModuleOptions(a.source)); err != nil {
		return nil, err
	}
	w := &pencodeEditorWindow{app: app, session: session}
	if err := a.native.Add(app, func() {
		session.Close()
		if err := session.FinishUpdate(); err != nil {
			fmt.Fprintln(os.Stderr, "启动更新失败：", err)
		}
		if index := slices.Index(a.windows, w); index >= 0 {
			a.windows = slices.Delete(a.windows, index, index+1)
			if len(a.windows) == 0 {
				a.wakeWindow.Store(nil)
			} else {
				a.wakeWindow.Store(a.windows[len(a.windows)-1].app)
			}
		}
	}); err != nil {
		return nil, err
	}
	a.windows = append(a.windows, w)
	a.wakeWindow.Store(app)
	return others, nil
}
