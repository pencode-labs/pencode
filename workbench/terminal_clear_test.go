package workbench

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pencode-labs/pencode/terminal"
)

func TestPencodeTerminalClearRedrawsMultilinePrompt(t *testing.T) {
	for _, name := range []string{"bash", "zsh"} {
		t.Run(name, func(t *testing.T) { testPencodeTerminalClearRedrawsMultilinePrompt(t, name) })
	}
}

func testPencodeTerminalClearRedrawsMultilinePrompt(t *testing.T, name string) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("POSIX shell interaction")
	}
	shell, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s is unavailable", name)
	}
	directory := t.TempDir()
	inputrc := filepath.Join(directory, "inputrc")
	if err := os.WriteFile(inputrc, []byte("set enable-bracketed-paste on\n\"\\C-l\": clear-screen\n"), 0600); err != nil {
		t.Fatal(err)
	}
	prompt := "\\[\\e[36m\\]╭─ ~/workspace/SimFamily\\[\\e[0m\\]\\n\\[\\e[32m\\]╰─> \\[\\e[0m\\]"
	args := []string{shell, "--noprofile", "--norc", "-i"}
	if name == "zsh" {
		args = []string{shell, "-f", "-i"}
		prompt = "%F{cyan}╭─ ~/workspace/SimFamily%f\n%F{green}╰─> %f"
	}
	term := "xterm-256color"
	item := &pencodeTerminal{screen: terminal.NewScreen(40, 8)}
	item.session = terminal.Attach(40, 8, nil, func() (terminal.Process, error) {
		process, _, err := terminal.OpenCommand(directory, args, map[string]*string{
			"PS1": &prompt, "PROMPT_COMMAND": nil, "INPUTRC": &inputrc, "TERM": &term,
		}, 40, 8)
		return process, err
	})
	t.Cleanup(func() {
		item.session.Close()
		select {
		case <-item.session.Done():
		case <-time.After(5 * time.Second):
			t.Error("shell did not exit")
		}
	})
	model := &workspaceModel{terminals: &pencodeTerminals{active: item, items: []*pencodeTerminal{item}}}
	waitFor := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			model.pollTerminals()
			model.prepareTerminalView()
			if check() {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("terminal condition timed out: %q", model.terminalView.value)
	}
	waitFor(func() bool { return item.ready && strings.Contains(model.terminalView.value, "╰─> ") })
	if !item.screen.BracketedPaste {
		t.Skipf("%s lacks bracketed paste support", name)
	}
	model.writeTerminal(item, []byte("printf '\\120\\105\\116_OLD\\n'\r"))
	waitFor(func() bool {
		return item.screen.BracketedPaste && strings.Contains(model.terminalView.value, "PEN_OLD")
	})
	model.writeTerminal(item, []byte("printf draft"))
	waitFor(func() bool { return strings.Contains(model.terminalView.value, "printf draft") })
	for range 2 {
		item.scroll = 2
		item.selection.active = true
		model.terminalAction(nil, "terminal-clear", "")
		waitFor(func() bool {
			_, y := item.screen.Cursor()
			return y == 1 && strings.HasPrefix(model.terminalView.value, "╭─ ~/workspace/SimFamily") && strings.Contains(model.terminalView.value, "╰─> printf draft")
		})
		if strings.Contains(model.terminalView.value, "PEN_OLD") || len(item.screen.History) != 0 || item.scroll != 0 || item.selection.active {
			t.Fatal("clear retained old output, scrollback, or selection")
		}
	}
	model.writeTerminal(item, []byte("\x7f!"))
	waitFor(func() bool { return strings.Contains(model.terminalView.value, "╰─> printf draf!") })
}

func TestPencodeTerminalClearWithoutLineEditor(t *testing.T) {
	for _, sequence := range []string{"output\r\nprompt> draft", "\x1b[?2004h\x1b[?1049hTUI"} {
		item := &pencodeTerminal{screen: terminal.NewScreen(40, 8), ready: true}
		item.screen.Feed([]byte(sequence))
		model := &workspaceModel{terminals: &pencodeTerminals{active: item}}
		revision := item.screen.Revision
		// There is no session: attempting to send input here would panic.
		model.terminalAction(nil, "terminal-clear", "")
		if item.screen.Alternate() && item.screen.Revision != revision {
			t.Fatal("clear changed a full-screen application")
		}
		if n := testing.AllocsPerRun(100, func() { model.terminalAction(nil, "terminal-clear", "") }); n != 0 {
			t.Fatalf("clear allocated %g", n)
		}
	}
}
