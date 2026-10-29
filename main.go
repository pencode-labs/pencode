package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/diiyw/onix"
	"github.com/diiyw/onix/project"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/remote"
	"github.com/pencode-labs/pencode/server"
	"github.com/pencode-labs/pencode/transport"
	"github.com/pencode-labs/pencode/workbench"
)

func main() {
	if handled, err := remote.RunAskpass(); handled {
		if err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--pencode-server" {
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
	if port > 65535 || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "invalid port or unexpected positional argument")
		os.Exit(2)
	}
	initial.SSH.Port = uint16(port)
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
	defer session.Close()
	dialer := connection.DefaultDialer(executable, session.Output.Channel("SSH"))
	if serverDirectory != "" {
		dialer.SSH.ServerDirectory = serverDirectory
	}
	session.ConfigureConnection(dialer.Dial, initial)
	if err := session.Register(&registry); err != nil {
		panic(err)
	}
	app := onix.NewApp()
	app.SetRenderMode(onix.RenderOnDemand)
	app.SetIdleEnabled(true)
	if err := OnixInstallSystems(app); err != nil {
		panic(err)
	}
	if err := app.AddSystems(onix.StagePostUpdate, session.ScrollSystem(), session.LayoutSystem()); err != nil {
		panic(err)
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
