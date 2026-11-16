package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onix-engine/onix/project"
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
	application := &pencodeApplication{plan: plan, source: source, executable: executable, serverDirectory: serverDirectory, recorder: recorder}
	defer application.native.Close()
	// Finder events are delivered to the active window of this application.
	desktopopen.Configure(application.openPath)
	_, err = application.open(pencodeWindowRequest{restoreID: restoreWindow, restore: !newWindow && flag.NArg() == 0 && !initial.Remote(), profile: initial, paths: flag.Args()})
	if err != nil {
		panic(err)
	}
	application.native.BetweenFrames = application.openPending
	if err := application.native.Run(); err != nil {
		panic(err)
	}
}
