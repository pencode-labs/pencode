package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onix-engine/onix"
	"github.com/onix-engine/onix/input"
	"github.com/onix-engine/onix/project"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/internal/desktopopen"
	"github.com/pencode-labs/pencode/internal/environment"
	"github.com/pencode-labs/pencode/internal/profiling"
	"github.com/pencode-labs/pencode/internal/systemproxy"
	"github.com/pencode-labs/pencode/internal/updater"
	"github.com/pencode-labs/pencode/internal/version"
	"github.com/pencode-labs/pencode/remote"
	"github.com/pencode-labs/pencode/server"
	"github.com/pencode-labs/pencode/transport"
	"github.com/pencode-labs/pencode/workbench"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		executable, _ := os.Executable()
		_, current, err := updater.Installation(executable)
		if err != nil {
			current = version.Value
		}
		fmt.Println(current)
		return
	}
	if handled, err := updater.RunHelper(); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	environment.Configure()
	if handled, err := remote.RunAskpass(); handled {
		if err != nil {
			os.Exit(1)
		}
		return
	}
	systemproxy.Configure()
	if len(os.Args) == 2 && os.Args[1] == "--pencode-server" {
		recorder, err := profiling.Start("server")
		if err != nil {
			fmt.Fprintln(os.Stderr, "Pencode profiling:", err)
		}
		defer recorder.Close()
		if err := server.Serve(context.Background(), transport.Stdio{}, server.Options{}); err != nil {
			panic(err)
		}
		return
	}
	var initial connection.Profile
	var port uint
	var serverDirectory string
	flag.StringVar(&initial.SSH.Host, "ssh", "", "SSH Host alias or user@host")
	flag.UintVar(&port, "ssh-port", 0, "SSH port; zero uses SSH config")
	flag.StringVar(&initial.SSH.IdentityFile, "ssh-identity", "", "SSH identity file")
	flag.StringVar(&initial.SSH.ConfigFile, "ssh-config", "", "SSH config file")
	flag.StringVar(&initial.Directory, "remote-path", "", "Remote workspace directory")
	flag.StringVar(&initial.SSH.ServerBinary, "server-binary", "", "Local standalone server binary for the remote platform")
	flag.StringVar(&initial.SSH.ServerRoot, "remote-server-directory", "", "Absolute remote server installation directory")
	flag.StringVar(&serverDirectory, "server-directory", "", "Local directory of platform server binaries")
	flag.Parse()
	if port > 65535 || flag.NArg() != 0 && (port != 0 || initial != (connection.Profile{})) {
		fmt.Fprintln(os.Stderr, "invalid port or local paths combined with a remote connection")
		os.Exit(2)
	}
	initial.SSH.Port = uint16(port)
	recorder, err := profiling.Start("gui")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Pencode profiling:", err)
	}
	defer recorder.Close()
	plan, source, err := project.OpenModuleProject()
	if err != nil {
		panic(err)
	}
	registry, err := OnixRegistry()
	if err != nil {
		panic(err)
	}
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	conn, err := transport.Start(executable, "--pencode-server")
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	backend, err := client.New(ctx, conn)
	cancel()
	if err != nil {
		panic(err)
	}
	session := workbench.NewSession(backend)
	session.OpenPaths(flag.Args()...)
	// Install before AppKit finishes launching so Finder's initial open event
	// is queued even before the workbench has entered its first scene.
	desktopopen.Configure(func(path string) { session.OpenPaths(path) })
	defer func() {
		session.Close()
		if err := session.FinishUpdate(); err != nil {
			fmt.Fprintln(os.Stderr, "启动更新失败：", err)
		}
	}()
	dialer := connection.DefaultDialer(executable, session.Output.Channel(remote.OutputChannelName))
	if serverDirectory != "" {
		dialer.SSH.ServerDirectory = serverDirectory
	}
	session.ConfigureConnection(dialer.Dial, initial)
	if err := session.Register(&registry); err != nil {
		panic(err)
	}
	app := onix.NewApp()
	app.AddInputHandler(input.PhaseGlobal, session.HandleFileDrop)
	app.SetRenderMode(onix.RenderOnDemand)
	app.SetIdleEnabled(true)
	if err := OnixInstallSystems(app); err != nil {
		panic(err)
	}
	if err := app.AddSystems(onix.StagePostUpdate, session.ScrollSystem(), session.LayoutSystem()); err != nil {
		panic(err)
	}
	if recorder != nil {
		if err := app.AddSystems(onix.StageRender, session.ProfilingSystem(recorder)); err != nil {
			panic(err)
		}
	}

	if err := OnixOpenWindow(app, plan.Spec, source); err != nil {
		panic(err)
	}
	if _, err := app.LoadModules[OnixContentProfile](plan, registry, OnixModuleOptions(source)); err != nil {
		panic(err)
	}
	if err := app.Run(); err != nil {
		panic(err)
	}
}
