package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/onix-engine/onix/project"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/internal/desktopopen"
	"github.com/pencode-labs/pencode/internal/environment"
	"github.com/pencode-labs/pencode/internal/profiling"
	"github.com/pencode-labs/pencode/internal/systemproxy"
	"github.com/pencode-labs/pencode/internal/updater"
	"github.com/pencode-labs/pencode/internal/version"
	"github.com/pencode-labs/pencode/internal/windowstate"
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
	var restoreWindow string
	var newWindow bool
	flag.BoolVar(&newWindow, "new-window", false, "Open a new window")
	flag.StringVar(&restoreWindow, "restore-window", "", "Restore a saved window")
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
	request := pencodeWindowRequest{Paths: flag.Args(), Profile: initial, RestoreID: restoreWindow, NewWindow: newWindow, Restore: !newWindow && flag.NArg() == 0 && !initial.Remote()}
	for i, path := range request.Paths {
		request.Paths[i] = workbench.LocalOpenPath(path)
	}
	for _, path := range []*string{&serverDirectory, &request.Profile.SSH.ConfigFile, &request.Profile.SSH.IdentityFile, &request.Profile.SSH.ServerBinary} {
		if *path != "" && !filepath.IsAbs(*path) {
			absolute, err := filepath.Abs(*path)
			if err != nil {
				panic(err)
			}
			*path = absolute
		}
	}
	request.ServerDirectory = serverDirectory
	application := &pencodeApplication{serverDirectory: serverDirectory}
	directory, err := os.UserConfigDir()
	if err != nil {
		panic(err)
	}
	application.inbox, err = windowstate.ForwardLaunch(filepath.Join(directory, "pencode", "launches"), request, func() {
		application.wakeWindow.Load().RequestRender()
	})
	if err != nil {
		panic(err)
	}
	if application.inbox == nil {
		return
	}
	defer application.inbox.Close()
	recorder, err := profiling.Start("gui")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Pencode profiling:", err)
	}
	defer recorder.Close()
	plan, source, err := project.OpenModuleProject()
	if err != nil {
		panic(err)
	}
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	application.plan, application.source, application.executable, application.recorder = plan, source, executable, recorder
	defer application.native.Close()
	// Finder events are delivered to the active window of this application.
	desktopopen.Configure(application.openPath)
	others, err := application.open(request)
	if err != nil {
		panic(err)
	}
	for _, id := range others {
		application.pending = append(application.pending, pencodeWindowRequest{RestoreID: id})
	}
	application.native.BetweenFrames = application.openPending
	if err := application.native.Run(); err != nil {
		panic(err)
	}
}
