package workbench

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/protocol"
)

type pencodePortCommand struct {
	op, key, value string
	remote         protocol.Port
	local          uint16
	id             uint64
}
type pencodePortRow struct {
	id                                                                            uint64
	key, remote, local, source, process, status, url, label, protocol, visibility string
	port, localPort                                                               uint16
	failed, allInterfaces                                                         bool
}
type pencodeForward struct {
	forward *client.Forward
	spec    pencodePortSpec
	row     pencodePortRow
}
type pencodePortsOptions struct {
	resume  *pencodePortsState
	sshPort uint16
}
type pencodePorts struct {
	backend             *client.Client
	commands            chan pencodePortCommand
	cancel              context.CancelFunc
	done                chan struct{}
	mu                  sync.Mutex
	revision, completed uint64
	rows                []pencodePortRow
	state               pencodePortsState
	message, selected   string
	failed              bool
}

func openExternalCommand(ctx context.Context, address string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(ctx, "open", address)
	case "windows":
		return exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", address)
	default:
		return exec.CommandContext(ctx, "xdg-open", address)
	}
}

func startPorts(backend *client.Client, options pencodePortsOptions, wake func()) *pencodePorts {
	ctx, cancel := context.WithCancel(context.Background())
	p := &pencodePorts{backend: backend, commands: make(chan pencodePortCommand, 8), cancel: cancel, done: make(chan struct{})}
	var state pencodePortsState
	if options.resume != nil {
		state = *options.resume
	}
	go func() {
		defer close(p.done)
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		forwards := make(map[string]*pencodeForward)
		suppressed := make(map[string]bool)
		for _, key := range state.Suppressed {
			suppressed[key] = true
		}
		defer func() {
			for _, f := range forwards {
				if f.forward != nil {
					f.forward.Close()
				}
			}
		}()
		var nextID, completed uint64
		var scratch []pencodePortRow
		message, selected, failed, dirty := "", "", false, false
		refreshRow := func(f *pencodeForward) {
			s, row := f.spec, &f.row
			row.key, row.port, row.label, row.protocol, row.allInterfaces = s.key(), s.Port, s.Label, s.Protocol, s.AllInterfaces
			row.remote = strconv.Itoa(int(s.Port))
			if s.Host != "localhost" {
				row.remote = row.key
			}
			if s.Label != "" {
				row.remote = s.Label + " (" + row.remote + ")"
			}
			row.source, row.visibility = "用户转发", "本机"
			if s.Auto {
				row.source = "自动转发"
			}
			if s.AllInterfaces {
				row.visibility = "局域网"
			}
			row.localPort = s.Local
			if f.forward != nil {
				_, port, _ := net.SplitHostPort(f.forward.Address())
				row.local = "127.0.0.1:" + port
				row.url = s.Protocol + "://" + row.local
				row.status, row.failed = "已转发", false
			}
		}
		bind := func(spec *pencodePortSpec, fallback bool) (*client.Forward, error) {
			f, err := backend.ForwardPort(ctx, protocol.Port{Host: spec.Host, Number: spec.Port}, client.ForwardOptions{Port: spec.Local, AllInterfaces: spec.AllInterfaces})
			if err != nil && fallback && spec.Local != 0 {
				f, err = backend.ForwardPort(ctx, protocol.Port{Host: spec.Host, Number: spec.Port}, client.ForwardOptions{AllInterfaces: spec.AllInterfaces})
			}
			if err == nil {
				_, number, _ := net.SplitHostPort(f.Address())
				actual, _ := strconv.ParseUint(number, 10, 16)
				spec.Local = uint16(actual)
			}
			return f, err
		}
		add := func(spec pencodePortSpec, restore bool) error {
			key := spec.key()
			if _, ok := forwards[key]; ok {
				selected = key
				return fmt.Errorf("%s 已在转发", key)
			}
			if len(forwards) >= pencodePortLimit {
				return fmt.Errorf("最多同时转发 %d 个端口", pencodePortLimit)
			}
			f, err := bind(&spec, true)
			if err != nil && !restore {
				return err
			}
			nextID++
			entry := &pencodeForward{forward: f, spec: spec, row: pencodePortRow{id: nextID}}
			refreshRow(entry)
			if err != nil {
				entry.row.status, entry.row.failed = err.Error(), true
			}
			forwards[key] = entry
			delete(suppressed, key)
			dirty = true
			return err
		}
		for _, spec := range state.Forwards {
			if err := add(spec, true); err != nil {
				message, failed = err.Error(), true
			}
		}
		publish := func() {
			scratch = scratch[:0]
			for _, f := range forwards {
				row := f.row
				if f.forward != nil {
					if err := f.forward.Err(); err != nil {
						row.status, row.failed = err.Error(), true
					}
				}
				scratch = append(scratch, row)
			}
			slices.SortFunc(scratch, func(a, b pencodePortRow) int {
				if a.port != b.port {
					return int(a.port) - int(b.port)
				}
				return strings.Compare(a.key, b.key)
			})
			if dirty {
				state.Forwards = make([]pencodePortSpec, 0, len(forwards))
				for _, row := range scratch {
					state.Forwards = append(state.Forwards, forwards[row.key].spec)
				}
				state.Suppressed = make([]string, 0, len(suppressed))
				for key := range suppressed {
					state.Suppressed = append(state.Suppressed, key)
				}
				slices.Sort(state.Suppressed)
				dirty = false
			}
			p.mu.Lock()
			changed := p.revision == 0 || p.message != message || p.failed != failed || p.completed != completed || !slices.Equal(p.rows, scratch) || !slices.Equal(p.state.Forwards, state.Forwards) || !slices.Equal(p.state.Suppressed, state.Suppressed)
			if changed {
				p.rows, scratch = scratch, p.rows
				p.message, p.failed, p.completed, p.selected, p.state = message, failed, completed, selected, state
				p.revision++
			}
			p.mu.Unlock()
			if changed && wake != nil {
				wake()
			}
		}
		scan := func() {
			requestCtx, stop := context.WithTimeout(ctx, 5*time.Second)
			ports, err := backend.ListeningPorts(requestCtx)
			stop()
			if err != nil {
				message, failed = "检测端口失败："+err.Error(), true
				return
			}
			if strings.HasPrefix(message, "检测端口失败：") {
				message, failed = "", false
			}
			present := make(map[string]protocol.Port, len(ports))
			for _, port := range ports {
				port.Host = normalizePortHost(port.Host)
				key := (pencodePortSpec{Host: port.Host, Port: port.Number}).key()
				if old, ok := present[key]; !ok || old.Process == "" {
					present[key] = port
				}
			}
			for key, f := range forwards {
				port, exists := present[key]
				if f.spec.Auto && !exists {
					if f.forward != nil {
						f.forward.Close()
					}
					delete(forwards, key)
					dirty = true
					continue
				}
				process := port.Process
				if port.PID != 0 {
					process += " (" + strconv.FormatUint(uint64(port.PID), 10) + ")"
				}
				f.row.process = process
			}
			for key := range suppressed {
				if _, ok := present[key]; !ok {
					delete(suppressed, key)
					dirty = true
				}
			}
			for key, port := range present {
				command := strings.Fields(port.Process)
				if len(command) > 0 && strings.HasPrefix(filepath.Base(command[0]), "sshd") {
					continue
				}
				if port.Number == 22 || options.sshPort != 0 && port.Number == options.sshPort || suppressed[key] {
					continue
				}
				if _, exists := forwards[key]; exists {
					continue
				}
				if port.PID == 0 {
					continue
				}
				if err := add(pencodePortSpec{Host: port.Host, Port: port.Number, Local: port.Number, Auto: true, Protocol: "http"}, false); err != nil {
					message, failed = err.Error(), true
				}
				if f := forwards[key]; f != nil {
					f.row.process = port.Process + " (" + strconv.FormatUint(uint64(port.PID), 10) + ")"
				}
			}
		}
		scan()
		publish()
		for {
			select {
			case <-ctx.Done():
				return
			case <-backend.Done():
				return
			case command := <-p.commands:
				message, failed, selected = "", false, ""
				entry := forwards[command.key]
				var err error
				switch command.op {
				case "add":
					spec := pencodePortSpec{Host: normalizePortHost(command.remote.Host), Port: command.remote.Number, Local: command.local, Protocol: "http"}
					err = add(spec, false)
					if err == nil {
						selected = spec.key()
					}
				case "stop":
					if entry != nil {
						if entry.forward != nil {
							entry.forward.Close()
						}
						delete(forwards, command.key)
						suppressed[command.key] = true
						dirty = true
					}
				case "refresh":
					for _, f := range forwards {
						if f.forward == nil {
							f.forward, err = bind(&f.spec, true)
							refreshRow(f)
							if err != nil {
								f.row.status, f.row.failed = err.Error(), true
							}
							dirty = true
						}
					}
					scan()
				case "label", "protocol", "local", "visibility":
					if entry == nil {
						err = fmt.Errorf("该端口已停止转发")
						break
					}
					spec := entry.spec
					switch command.op {
					case "label":
						spec.Label = command.value
					case "protocol":
						if command.value != "http" && command.value != "https" {
							err = protocol.ErrProtocol
						} else {
							spec.Protocol = command.value
						}
					case "local":
						spec.Local = command.local
					case "visibility":
						spec.AllInterfaces = !spec.AllInterfaces
					}
					if err != nil {
						break
					}
					if command.op == "local" || command.op == "visibility" {
						old := entry.forward
						if old != nil && spec.Local == entry.spec.Local && command.op == "local" {
							break
						}
						if old != nil && spec.Local == entry.spec.Local {
							old.Close()
							entry.forward = nil
						}
						var fresh *client.Forward
						fresh, err = bind(&spec, false)
						if err != nil {
							if entry.forward == nil {
								var rollbackErr error
								entry.forward, rollbackErr = bind(&entry.spec, false)
								refreshRow(entry)
								if rollbackErr != nil {
									entry.row.local, entry.row.url = "", ""
									entry.row.status, entry.row.failed = rollbackErr.Error(), true
									err = fmt.Errorf("%w；恢复原转发失败：%v", err, rollbackErr)
								}
								dirty = true
							}
							break
						}
						if old != nil {
							old.Close()
						}
						entry.forward = fresh
					}
					entry.spec = spec
					refreshRow(entry)
					selected = command.key
					dirty = true
				case "open":
					if entry != nil && entry.row.url != "" {
						openCtx, stop := context.WithTimeout(ctx, 5*time.Second)
						err = openExternalCommand(openCtx, entry.row.url).Run()
						stop()
						if err != nil {
							err = fmt.Errorf("无法打开浏览器：%w", err)
						}
					}
				}
				if err != nil {
					message, failed = err.Error(), true
				}
				completed = command.id
				publish()
			case <-ticker.C:
				scan()
				publish()
			}
		}
	}()
	return p
}
