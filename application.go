package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"time"

	"github.com/onix-engine/onix"
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/project"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/internal/profiling"
	"github.com/pencode-labs/pencode/remote"
	"github.com/pencode-labs/pencode/transport"
	"github.com/pencode-labs/pencode/workbench"
)

type pencodeWindowRequest struct {
	paths     []string
	restoreID string
	profile   connection.Profile
	restore   bool
}

type pencodeEditorWindow struct {
	app     *onix.App
	session *workbench.Session
}

type pencodeApplication struct {
	source                      fs.FS
	plan                        *project.ProjectPlan
	recorder                    *profiling.Recorder
	windows                     []*pencodeEditorWindow
	pending                     []pencodeWindowRequest
	executable, serverDirectory string
	native                      onix.Application
}

func (a *pencodeApplication) queueWindow(paths []string, profile connection.Profile) error {
	a.pending = append(a.pending, pencodeWindowRequest{paths: slices.Clone(paths), profile: profile})
	return nil
}

func (a *pencodeApplication) openPath(path string) {
	for _, w := range a.windows {
		if w.app.Context.IsFocused() {
			w.session.OpenPaths(path)
			return
		}
	}
	if len(a.windows) != 0 {
		a.windows[len(a.windows)-1].session.OpenPaths(path)
	} else {
		_ = a.queueWindow([]string{path}, connection.Profile{})
	}
}

func (a *pencodeApplication) openPending() error {
	for len(a.pending) != 0 {
		request := a.pending[0]
		a.pending[0] = pencodeWindowRequest{}
		a.pending = a.pending[1:]
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
	others, err = session.ConfigureWindows(request.restoreID, request.restore)
	if err != nil {
		return nil, err
	}
	session.SetWindowOpener(a.queueWindow)
	session.OpenPaths(request.paths...)
	dialer := connection.DefaultDialer(a.executable, session.Output.Channel(remote.OutputChannelName))
	if a.serverDirectory != "" {
		dialer.SSH.ServerDirectory = a.serverDirectory
	}
	session.ConfigureConnection(dialer.Dial, request.profile)
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
		}
	}); err != nil {
		return nil, err
	}
	a.windows = append(a.windows, w)
	return others, nil
}
