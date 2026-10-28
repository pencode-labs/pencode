package workbench

import (
	"net"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/diiyw/onix/ecs"
	"github.com/diiyw/onix/input"
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/scene"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/connection"
	"github.com/pencode-labs/pencode/protocol"
	"github.com/pencode-labs/pencode/remote"
)

func waitPorts(t *testing.T, p *pencodePorts, ready func([]pencodePortRow, string) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.mu.Lock()
		ok := ready(p.rows, p.message)
		p.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			p.mu.Lock()
			t.Logf("ports=%+v, message=%s", p.rows, p.message)
			p.mu.Unlock()
			t.Fatal("port operation did not complete")
		}
		time.Sleep(time.Millisecond)
	}
}
func portTestCommand(t *testing.T, p *pencodePorts, command pencodePortCommand) {
	t.Helper()
	p.mu.Lock()
	command.id = p.completed + 1
	p.mu.Unlock()
	p.commands <- command
	deadline := time.Now().Add(5 * time.Second)
	for {
		p.mu.Lock()
		done := p.completed == command.id
		p.mu.Unlock()
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("port command did not complete", command.op)
		}
		time.Sleep(time.Millisecond)
	}
}
func portRows(t *testing.T, p *pencodePorts) []pencodePortRow {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.rows)
}

// Existing host listeners belong to the surrounding development environment.
// Keep automatic forwarding enabled while isolating the ports created by a test.
func portTestState(t *testing.T, backend *client.Client) pencodePortsState {
	t.Helper()
	state := pencodePortsState{}
	ports, err := backend.ListeningPorts(t.Context())
	if err != nil {
		return state
	}
	for _, port := range ports {
		state.Suppressed = append(state.Suppressed, (pencodePortSpec{Host: port.Host, Port: port.Number}).key())
	}
	return state
}

func portPresent(rows []pencodePortRow, key string) bool {
	return slices.ContainsFunc(rows, func(row pencodePortRow) bool { return row.key == key })
}

func portTestRow(t *testing.T, p *pencodePorts, key string) pencodePortRow {
	t.Helper()
	for _, row := range portRows(t, p) {
		if row.key == key {
			return row
		}
	}
	t.Fatalf("forward %s is missing", key)
	return pencodePortRow{}
}

func TestPortsPanelInlineInteractions(t *testing.T) {
	app, m, _, state := languageInteractionFixture(t)
	world := m.fontResources
	node, ok := world.HTMLNode(m.surface, "status-ports")
	if !ok {
		t.Fatal("missing Ports button")
	}
	if bounds, ok := node.Bounds(); ok && bounds.Height > 0 {
		t.Fatal("local workspace shows Ports")
	}
	m.session.ConfigureConnection(nil, connection.Profile{})
	m.session.connection.workspaceProfile = connection.Profile{SSH: remote.Target{Host: "development"}}
	m.portUI.owner = pencodePortsIdentity{m.session.connection.workspaceProfile.Endpoint(), m.projectPath}
	m.portUI.owned, m.portUI.resumeReady = true, true
	m.portUI.resume = portTestState(t, m.backend)
	m.pollPorts()
	t.Cleanup(m.closePorts)
	markPencodeRootDirtyByModel(world, m)
	languageInteractionStep(t, app)
	editorContextClick(t, app, m, state, "status-ports")
	editorContextClick(t, app, m, state, "ports-new")
	if m.portUI.mode != "new" {
		t.Fatal("Forward Port did not open inline input")
	}
	press := func(key input.KeyCode) {
		t.Helper()
		state.Enqueue(input.Event{Kind: input.EventKeyDown, Key: key})
		state.Enqueue(input.Event{Kind: input.EventKeyUp, Key: key})
		languageInteractionStep(t, app)
	}
	enter := func(value string) { t.Helper(); m.portEvent("ports-input", value); press(input.KeyEnter) }
	enter("65536")
	if m.portUI.message == "" || m.portUI.mode != "new" {
		t.Fatal("invalid inline port was accepted")
	}
	world.Focus(m.editor.control)
	m.portEvent("ports-input", "service.internal:3000")
	press(input.KeyEnter)
	if m.portUI.pending != 0 || m.portUI.mode != "new" {
		t.Fatal("port input intercepted Enter while the editor had focus")
	}
	editorContextClick(t, app, m, state, "ports-input")
	enter("service.internal:3000")
	waitPorts(t, m.ports, func(rows []pencodePortRow, _ string) bool { return portPresent(rows, "service.internal:3000") })
	languageInteractionStep(t, app)
	if row := m.portRow("service.internal:3000"); m.portUI.mode != "" || row == nil || row.source != "用户转发" {
		t.Fatalf("manual forward missing: %+v", m.portUI)
	}
	row := *m.portRow("service.internal:3000")
	address := row.local
	if n := testing.AllocsPerRun(100, func() { m.pollPorts() }); n != 0 {
		t.Fatalf("idle ports allocated %g", n)
	}
	clipboard := ""
	state.SetClipboard(func(text string) bool { clipboard = text; return true }, func() (string, bool) { return clipboard, true })
	editorContextClick(t, app, m, state, "ports-copy:"+row.key)
	if clipboard != "http://"+address {
		t.Fatal("copy did not use forwarded URL", clipboard)
	}
	r := editorContextBounds(t, m, portRowRef(row)+"-row")
	scale := float64(m.settings.Zoom) / 100
	x, y := float64(r.X+8)*scale, float64(r.Y+r.Height/2)*scale
	state.Enqueue(input.Event{Kind: input.EventMouseDown, Button: input.MouseRight, X: x, Y: y})
	state.Enqueue(input.Event{Kind: input.EventMouseUp, Button: input.MouseRight, X: x, Y: y})
	languageInteractionStep(t, app)
	for _, ref := range []string{"ports-context", "ports-more", "ports-auto", "ports-add", "ports-restore"} {
		if _, ok := world.HTMLNode(m.surface, ref); ok {
			t.Fatalf("removed port control %s is still present", ref)
		}
	}
	editorContextClick(t, app, m, state, portRowRef(row))
	press(input.KeyF2)
	if m.portUI.mode != "label" {
		t.Fatal("F2 did not edit label")
	}
	enter("开发服务器")
	waitPorts(t, m.ports, func(rows []pencodePortRow, _ string) bool {
		return slices.ContainsFunc(rows, func(row pencodePortRow) bool {
			return row.key == "service.internal:3000" && row.label == "开发服务器"
		})
	})
	languageInteractionStep(t, app)
	if control, ok := world.HTMLControl(m.surface, portRowRef(row)); !ok || world.FocusedControl() != control {
		t.Fatal("confirming an edit did not return keyboard focus to the port row")
	}
	editorContextClick(t, app, m, state, "ports-local:"+row.key)
	if m.portUI.mode != "local" {
		t.Fatal("local address edit missing")
	}
	enter("0")
	waitPorts(t, m.ports, func(rows []pencodePortRow, _ string) bool {
		return slices.ContainsFunc(rows, func(r pencodePortRow) bool { return r.key == row.key && r.local != address })
	})
	languageInteractionStep(t, app)
	if conn, err := net.DialTimeout("tcp4", address, time.Second); err == nil {
		conn.Close()
		t.Fatal("old listener survived local-port change")
	}
	editorContextClick(t, app, m, state, "ports-protocol:"+row.key)
	waitPorts(t, m.ports, func(rows []pencodePortRow, _ string) bool {
		return slices.ContainsFunc(rows, func(r pencodePortRow) bool { return r.key == row.key && strings.HasPrefix(r.url, "https://") })
	})
	languageInteractionStep(t, app)
	editorContextClick(t, app, m, state, "ports-copy:"+row.key)
	if !strings.HasPrefix(clipboard, "https://") {
		t.Fatal("copy ignored selected HTTPS protocol", clipboard)
	}
	editorContextClick(t, app, m, state, "ports-visibility:"+row.key)
	waitPorts(t, m.ports, func(rows []pencodePortRow, _ string) bool {
		return slices.ContainsFunc(rows, func(r pencodePortRow) bool { return r.key == row.key && r.allInterfaces })
	})
	languageInteractionStep(t, app)
	before := *m.portRow(row.key)
	m.closePorts()
	m.pollPorts()
	waitPorts(t, m.ports, func(rows []pencodePortRow, _ string) bool { return portPresent(rows, "service.internal:3000") })
	languageInteractionStep(t, app)
	after := *m.portRow(row.key)
	if before.local != after.local || after.label != "开发服务器" || after.protocol != "https" || !after.allInterfaces {
		t.Fatalf("reconnect lost forward: before=%+v after=%+v", before, after)
	}
	editorContextClick(t, app, m, state, portRowRef(after))
	press(input.KeyF2)
	press(input.KeyEscape)
	if m.portUI.mode != "" || m.bottom.active != pencodePanelPorts {
		t.Fatal("Escape did not cancel inline edit independently")
	}
	editorContextClick(t, app, m, state, "ports-stop:"+row.key)
	waitPorts(t, m.ports, func(rows []pencodePortRow, _ string) bool { return !portPresent(rows, row.key) })
	if conn, err := net.DialTimeout("tcp4", after.local, time.Second); err == nil {
		conn.Close()
		t.Fatal("stopped forward still listens")
	}
	for port := uint16(3100); port < 3116; port++ {
		portTestCommand(t, m.ports, pencodePortCommand{op: "add", remote: protocol.Port{Host: "service.internal", Number: port}})
	}
	languageInteractionStep(t, app)
	heading := editorContextBounds(t, m, "ports-heading")
	body := editorContextBounds(t, m, "ports-list")
	if body.Y < heading.Y+heading.Height {
		t.Fatal("list scrolling overlaps the heading")
	}
	firstKey := "service.internal:3100"
	source := editorContextBounds(t, m, "ports-source:"+firstKey)
	stop := editorContextBounds(t, m, "ports-stop:"+firstKey)
	if source.Width < 48 || source.Height < 16 || source.X+source.Width > stop.X {
		t.Fatalf("source label is clipped or overlaps its action: source=%+v stop=%+v", source, stop)
	}
	var headingVisual ecs.Entity
	var headingY float32
	for entity, sprite := range ecs.Query[scene.Sprite2D](app.World).All {
		position := ecs.GetRef[scene.UIGlobalTransform](app.World, entity)
		if sprite.Ref().Width == heading.Width && position != nil && position.Matrix[12] == heading.X && position.Matrix[13] >= heading.Y && position.Matrix[13] < heading.Y+heading.Height {
			headingVisual, headingY = entity, position.Matrix[13]
			break
		}
	}
	listNode, _ := world.HTMLNode(m.surface, "ports-list")
	scrollEntity, ok := listNode.ScrollContainer()
	if !ok || headingVisual == 0 {
		t.Fatalf("missing list scroll container or rendered heading: scroll=%v heading=%d headingBounds=%+v body=%+v", ok, headingVisual, heading, body)
	}
	scroll := module.ContentMut[scene.UIScroll](world, scrollEntity)
	if scroll.ContentSize[1] <= scroll.ViewportSize[1] {
		t.Fatal("port rows did not overflow the list viewport")
	}
	firstControl, _ := world.HTMLControl(m.surface, "ports-row:"+firstKey)
	firstY := ecs.GetRef[scene.UIGlobalTransform](app.World, firstControl).Matrix[13]
	scroll.Position[1] = -60
	languageInteractionStep(t, app)
	if ecs.GetRef[scene.UIGlobalTransform](app.World, headingVisual).Matrix[13] != headingY || ecs.GetRef[scene.UIGlobalTransform](app.World, firstControl).Matrix[13] != firstY-60 {
		t.Fatal("scrolling the list moved the heading or failed to move the rows")
	}
	m.session.connection.workspaceProfile = connection.Profile{}
	m.pollPorts()
	if m.ports != nil || m.bottom.active != pencodePanelClosed {
		t.Fatal("local switch kept remote Ports active")
	}
}

func TestPortsAutoForwardProcessConflictAndStopSuppression(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("remote process discovery requires Linux or macOS")
	}
	backend := newPencodeBackend(t)
	initial := portTestState(t, backend)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	number := uint16(listener.Addr().(*net.TCPAddr).Port)
	key := (pencodePortSpec{Host: "localhost", Port: number}).key()
	p := startPorts(backend, pencodePortsOptions{resume: &initial}, nil)
	defer func() { p.cancel(); <-p.done }()
	hasPort := func(rows []pencodePortRow) bool {
		return slices.ContainsFunc(rows, func(row pencodePortRow) bool { return row.key == key })
	}
	waitPorts(t, p, func(rows []pencodePortRow, _ string) bool { return hasPort(rows) })
	for _, row := range portRows(t, p) {
		if row.key == key && (row.local == listener.Addr().String() || row.source != "自动转发" || row.process == "") {
			t.Errorf("automatic process forward is incomplete: %+v", row)
		}
	}
	portTestCommand(t, p, pencodePortCommand{op: "stop", key: key})
	portTestCommand(t, p, pencodePortCommand{op: "refresh"})
	if hasPort(portRows(t, p)) {
		t.Fatal("automatic scan recreated a stopped forward")
	}
	second, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	secondNumber := uint16(second.Addr().(*net.TCPAddr).Port)
	secondPresent := func(rows []pencodePortRow) bool {
		return slices.ContainsFunc(rows, func(row pencodePortRow) bool { return row.port == secondNumber })
	}
	portTestCommand(t, p, pencodePortCommand{op: "refresh"})
	if !secondPresent(portRows(t, p)) {
		t.Fatal("new process listener was not automatically forwarded")
	}
	second.Close()
	portTestCommand(t, p, pencodePortCommand{op: "refresh"})
	if secondPresent(portRows(t, p)) {
		t.Fatal("automatic forward survived remote process listener close")
	}
}

func TestPortsStateLivesOnlyInSession(t *testing.T) {
	backend := newPencodeBackend(t)
	state := portTestState(t, backend)
	p := startPorts(backend, pencodePortsOptions{resume: &state}, nil)
	t.Cleanup(func() { p.cancel(); <-p.done })
	portTestCommand(t, p, pencodePortCommand{op: "add", remote: protocol.Port{Host: "service.internal", Number: 3000}})
	portTestCommand(t, p, pencodePortCommand{op: "label", key: "service.internal:3000", value: "app"})
	portTestCommand(t, p, pencodePortCommand{op: "protocol", key: "service.internal:3000", value: "https"})
	before := portTestRow(t, p, "service.internal:3000")
	p.cancel()
	<-p.done
	state = p.state
	p = startPorts(backend, pencodePortsOptions{resume: &state}, nil)
	waitPorts(t, p, func(rows []pencodePortRow, _ string) bool { return portPresent(rows, "service.internal:3000") })
	resumed := portTestRow(t, p, "service.internal:3000")
	if resumed.local != before.local || resumed.label != "app" || resumed.protocol != "https" {
		t.Fatalf("reconnect lost session state: %+v", resumed)
	}
	p.cancel()
	<-p.done
	state = portTestState(t, backend)
	p = startPorts(backend, pencodePortsOptions{resume: &state}, nil)
	portTestCommand(t, p, pencodePortCommand{op: "refresh"})
	if portPresent(portRows(t, p), "service.internal:3000") {
		t.Fatal("new session restored forwards from a previous session")
	}
}

func TestPortsAddresses(t *testing.T) {
	for _, value := range []string{"3000", "localhost:3000", "127.0.0.1:3000", "[::1]:3000", "10.0.0.2:3000", "service.internal:5432"} {
		if _, err := parsePortAddress(value); err != nil {
			t.Errorf("valid address %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "0", "65536", "localhost:", "http://localhost:3000", "bad host:3000", "::1:3000"} {
		if _, err := parsePortAddress(value); err == nil {
			t.Errorf("accepted invalid address %q", value)
		}
	}

}

func TestPortsExplicitLocalConflictAndVisibility(t *testing.T) {
	backend := newPencodeBackend(t)
	initial := portTestState(t, backend)
	p := startPorts(backend, pencodePortsOptions{resume: &initial}, nil)
	defer func() { p.cancel(); <-p.done }()
	portTestCommand(t, p, pencodePortCommand{op: "add", remote: protocol.Port{Host: "service.internal", Number: 3000}})
	portTestCommand(t, p, pencodePortCommand{op: "add", remote: protocol.Port{Host: "other.internal", Number: 3000}})
	first := portTestRow(t, p, "other.internal:3000")
	before := portTestRow(t, p, "service.internal:3000")
	if first.local == before.local {
		t.Fatal("same remote port on different hosts shares a local listener")
	}
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	portTestCommand(t, p, pencodePortCommand{op: "local", key: before.key, local: uint16(occupied.Addr().(*net.TCPAddr).Port)})
	p.mu.Lock()
	failed := p.failed && p.message != ""
	p.mu.Unlock()
	if !failed || portTestRow(t, p, before.key).local != before.local {
		t.Fatal("occupied explicit local port did not preserve the original forward")
	}
	probe, err := net.Listen("tcp4", before.local)
	if err == nil {
		probe.Close()
		t.Fatal("original listener was released after a failed change")
	}
	portTestCommand(t, p, pencodePortCommand{op: "visibility", key: before.key})
	after := portTestRow(t, p, before.key)
	if !after.allInterfaces || after.local != before.local || after.visibility != "局域网" {
		t.Fatalf("visibility change lost the local port: %+v", after)
	}
	portTestCommand(t, p, pencodePortCommand{op: "visibility", key: before.key})
	if after = portTestRow(t, p, before.key); after.allInterfaces || after.local != before.local {
		t.Fatalf("failed to restore loopback visibility: %+v", after)
	}
}

func TestPortsBackendReplacementAndWorkspaceSwitch(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	backend := newPencodeBackend(t)
	session := NewSession(backend)
	session.connection = &pencodeConnection{workspaceProfile: connection.Profile{SSH: remote.Target{Host: "development"}}}
	m := &workspaceModel{session: session, backend: backend, projectPath: t.TempDir()}
	defer m.closePorts()
	m.portUI.owner = pencodePortsIdentity{m.session.connection.workspaceProfile.Endpoint(), m.projectPath}
	m.portUI.owned, m.portUI.resumeReady = true, true
	m.portUI.resume = portTestState(t, backend)
	m.pollPorts()
	portTestCommand(t, m.ports, pencodePortCommand{op: "add", remote: protocol.Port{Host: "service.internal", Number: 3000}})
	before := portTestRow(t, m.ports, "service.internal:3000")
	backend.Close()
	m.pollPorts()
	if m.ports != nil {
		t.Fatal("disconnected backend kept the ports worker")
	}
	m.backend = newPencodeBackend(t)
	m.pollPorts()
	waitPorts(t, m.ports, func(rows []pencodePortRow, _ string) bool { return portPresent(rows, "service.internal:3000") })
	if after := portTestRow(t, m.ports, "service.internal:3000"); after.local != before.local || after.key != before.key {
		t.Fatalf("backend replacement lost the forward: %+v", after)
	}
	previous := m.ports
	m.projectPath = t.TempDir()
	m.pollPorts()
	portTestCommand(t, m.ports, pencodePortCommand{op: "refresh"})
	if m.ports == previous || portPresent(portRows(t, m.ports), before.key) {
		t.Fatal("workspace switch reused the previous workspace's forwards")
	}
	probe, err := net.Listen("tcp4", before.local)
	if err != nil {
		t.Fatal("workspace switch did not release the old local listener", err)
	}
	probe.Close()
}
