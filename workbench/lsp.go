package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/projects/pencode/extensions"
	"github.com/diiyw/onix/projects/pencode/lsp"
)

type pencodeLanguageKey struct{ root, id string }
type pencodeLanguageState struct {
	servers          []*extensions.LanguageServer
	sessions         map[pencodeLanguageKey]*pencodeLanguageSession
	documents        map[*openFile]*pencodeLanguageDocument
	events           chan pencodeLanguageEvent
	semanticDocument *pencodeLanguageDocument
	panel            pencodeLanguagePanel
}
type pencodeLanguageDocument struct {
	file           *openFile
	project        *pencodeProject
	root           string
	uri            lsp.URI
	content, saved string
	version        int
	semantic       *pencodeSemanticTokens
	seen           bool
	servers        map[*pencodeLanguageSession]pencodeLanguageSynced
	diagnostics    map[*pencodeLanguageSession][]lsp.Diagnostic
}
type pencodeLanguageSynced struct {
	version      int
	saveRevision uint64
	update       *pencodeLanguageUpdate
}
type pencodeLanguageSession struct {
	key    pencodeLanguageKey
	server *extensions.LanguageServer
	client *lsp.Client // published on UI thread by ready event
	jobs   chan pencodeLanguageJob
	cancel context.CancelFunc
	done   chan struct{}
	failed bool
}

// At most one queued snapshot per document is retained while a request runs.
// Updating it drops obsolete source strings rather than filling the job queue.
type pencodeLanguageUpdate struct {
	mu    sync.Mutex
	taken bool
	job   pencodeLanguageJob
}

func (u *pencodeLanguageUpdate) replace(job pencodeLanguageJob) bool {
	if u == nil {
		return false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.taken || job.save {
		return false
	}
	if u.job.save && !job.save {
		job.save = true
		job.saved = u.job.saved
	}
	u.job = job
	return true
}
func (u *pencodeLanguageUpdate) take() pencodeLanguageJob {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.taken = true
	job := u.job
	u.job = pencodeLanguageJob{}
	return job
}

type pencodeLanguageJob struct {
	update                 *pencodeLanguageUpdate
	document               *pencodeLanguageDocument
	uri                    lsp.URI
	content, saved, method string
	version                int
	close, save            bool
	semantic               bool
	willSave               bool
	notify                 bool
	context                context.Context
	params                 any
	token                  uint64
	offset                 int
	hover                  bool
	automatic              bool
}
type pencodeLanguageEvent struct {
	session *pencodeLanguageSession
	client  *lsp.Client
	method  string
	params  json.RawMessage
	result  json.RawMessage
	job     pencodeLanguageJob
	err     error
	reply   chan pencodeLanguageReply
}
type pencodeLanguageReply struct {
	result any
	err    error
}

func (model *workspaceModel) importLanguageServers(servers []extensions.LanguageServer) {
	if len(servers) == 0 {
		return
	}
	if model.languages == nil {
		model.languages = &pencodeLanguageState{sessions: make(map[pencodeLanguageKey]*pencodeLanguageSession), documents: make(map[*openFile]*pencodeLanguageDocument), events: make(chan pencodeLanguageEvent, 128)}
	}
	for i := range servers {
		servers[i].Output = model.outputStore().Channel("Language Server: " + servers[i].Label)
		model.languages.servers = append(model.languages.servers, &servers[i])
	}
}
func (model *workspaceModel) removeLanguageServers(prefix string) {
	state := model.languages
	if state == nil {
		return
	}
	kept := state.servers[:0]
	for _, server := range state.servers {
		if !strings.HasPrefix(server.ID, prefix) {
			kept = append(kept, server)
		}
	}
	clear(state.servers[len(kept):])
	state.servers = kept
	for key, session := range state.sessions {
		if !strings.HasPrefix(key.id, prefix) {
			continue
		}
		session.cancel()
		delete(state.sessions, key)
		model.clearSessionProblems(session)
		for _, doc := range state.documents {
			delete(doc.servers, session)
			delete(doc.diagnostics, session)
			if doc.semantic != nil && doc.semantic.session == session {
				doc.semantic = nil
			}
		}
	}
	model.dismissLanguagePanel()
	model.cancelLanguageAutomatic()
	model.cancelLanguageHover()
	model.dismissLanguageContext()
	state.panel = pencodeLanguagePanel{}
}
func (model *workspaceModel) closeLanguageServers() {
	model.closeReferenceWorker()
	model.closeHoverWorker()
	model.cancelLanguageAutomatic()
	model.cancelLanguageHover()
	model.dismissLanguageContext()
	if state := model.languages; state != nil {
		model.dismissLanguagePanel()
		for _, session := range state.sessions {
			session.cancel()
		}
		model.languages = nil
		clear(model.bottom.diagnostics)
		model.bottom.problemsDirty = true
	}
}
func (model *workspaceModel) startLanguageSession(root string, server *extensions.LanguageServer) *pencodeLanguageSession {
	state := model.languages
	key := pencodeLanguageKey{root, server.ID}
	if existing := state.sessions[key]; existing != nil {
		return existing
	}
	ctx, cancel := context.WithCancel(context.Background())
	session := &pencodeLanguageSession{key: key, server: server, jobs: make(chan pencodeLanguageJob, 64), cancel: cancel, done: make(chan struct{})}
	state.sessions[key] = session
	wake := model.backgroundWake()
	go func() {
		defer close(session.done)
		publish := func(event pencodeLanguageEvent) bool {
			event.session = session
			select {
			case state.events <- event:
				if wake != nil {
					wake()
				}
				return true
			case <-ctx.Done():
				return false
			}
		}
		handler := func(requestCtx context.Context, method string, params json.RawMessage) (any, error) {
			event := pencodeLanguageEvent{method: method, params: params}
			switch method {
			case "workspace/applyEdit", "window/showMessageRequest", "window/showDocument":
				event.reply = make(chan pencodeLanguageReply, 1)
			case "textDocument/publishDiagnostics", "window/logMessage", "window/showMessage", "$/progress", "telemetry/event", "workspace/semanticTokens/refresh":
			default:
				return nil, &lsp.ResponseError{Code: lsp.MethodNotFound, Message: method}
			}
			if !publish(event) {
				return nil, ctx.Err()
			}
			if event.reply == nil {
				return nil, nil
			}
			select {
			case reply := <-event.reply:
				return reply.result, reply.err
			case <-requestCtx.Done():
				return nil, requestCtx.Err()
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		startup, stop := context.WithTimeout(ctx, 10*time.Second)
		client, err := server.Start(startup, root, handler)
		stop()
		if err != nil {
			publish(pencodeLanguageEvent{method: "failed", err: err})
			return
		}
		defer func() {
			shutdown, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			_ = client.Shutdown(shutdown)
		}()
		if !publish(pencodeLanguageEvent{method: "ready", client: client}) {
			return
		}
		session.runJobs(ctx, client, publish)
	}()
	return session
}

// Keep document mutations and request sends on this ordered dispatcher. Only
// response waits run concurrently, so slow semantic tokens cannot hold edits,
// hover or completion behind a server response. Concurrency is bounded per LSP.
const pencodeLanguageConcurrency = 8

var errPencodeLanguageBusy = errors.New("语言服务请求繁忙，请稍后重试")

func (session *pencodeLanguageSession) runJobs(ctx context.Context, client *lsp.Client, publish func(pencodeLanguageEvent) bool) {
	var running sync.WaitGroup
	completed := make(chan struct{}, pencodeLanguageConcurrency)
	var pending [64]pencodeLanguageJob
	head, count, active := 0, 0, 0
	defer func() {
		session.cancel()
		running.Wait()
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		var job pencodeLanguageJob
		if count > 0 && active < pencodeLanguageConcurrency {
			job, pending[head] = pending[head], pencodeLanguageJob{}
			head, count = (head+1)%len(pending), count-1
		} else {
			select {
			case <-ctx.Done():
				return
			case <-client.Done():
				publish(pencodeLanguageEvent{method: "failed", err: client.Err()})
				return
			case <-completed:
				active--
				continue
			case job = <-session.jobs:
			}
		}
		if job.update != nil {
			job = job.update.take()
		}
		// Waiting requests have a fixed-size queue. Notifications keep flowing
		// even when all response slots are occupied; no goroutines wait for slots.
		if job.method != "" && !job.notify && !job.close && !job.willSave && active == pencodeLanguageConcurrency {
			if count < len(pending) {
				pending[(head+count)%len(pending)] = job
				count++
			} else if !publish(pencodeLanguageEvent{method: "result", job: job, err: errPencodeLanguageBusy}) {
				return
			}
			continue
		}
		jobCtx := ctx
		if job.context != nil {
			jobCtx = job.context
		}
		request, stop := context.WithTimeout(jobCtx, 30*time.Second)
		cancelSession := context.AfterFunc(ctx, stop)
		finish := func() { cancelSession(); stop() }
		var err error
		switch {
		case job.close:
			err = client.CloseDocument(request, job.uri)
		case job.willSave:
			err = client.WillSave(request, job.uri)
		case job.notify:
			err = client.Notify(request, job.method, job.params)
		case job.method == "":
			err = client.SyncDocument(request, job.uri, session.server.LanguageID, job.content, job.version)
			if err == nil && job.save {
				err = client.DidSaveText(request, job.uri, job.saved)
			}
		default:
			version, exists := client.DocumentVersion(job.uri)
			if job.document != nil && job.method != "workspace/executeCommand" && (!exists || version != job.version) {
				err = &lsp.ResponseError{Code: lsp.ContentModified, Message: "document changed before request"}
				break
			}
			call, beginErr := client.BeginCall(request, job.method, job.params)
			if beginErr != nil {
				err = beginErr
				break
			}
			active++
			running.Add(1)
			go func() {
				defer running.Done()
				defer finish()
				var result json.RawMessage
				err := call.Wait(request, &result)
				publish(pencodeLanguageEvent{method: "result", job: job, result: result, err: err})
				select {
				case completed <- struct{}{}:
				case <-ctx.Done():
				}
			}()
			continue
		}
		finish()
		if job.method != "" && !job.notify || err != nil {
			if !publish(pencodeLanguageEvent{method: "result", job: job, err: err}) {
				return
			}
		}
	}
}

func (model *workspaceModel) syncLanguageDocuments() {
	state := model.languages
	if state == nil {
		return
	}
	for _, doc := range state.documents {
		doc.seen = false
	}
	visit := func(root string, project *pencodeProject, files map[string]*openFile) {
		for _, file := range files {
			if !isTextFile(file.Name) {
				continue
			}
			doc := state.documents[file]
			if doc == nil {
				matched := false
				for _, server := range state.servers {
					if server.Match(file.Name) {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
				doc = &pencodeLanguageDocument{file: file, project: project, root: root, uri: lsp.FileURI(filepath.Join(root, filepath.FromSlash(file.Path))), content: file.Content, saved: file.savedContent, version: 1, servers: make(map[*pencodeLanguageSession]pencodeLanguageSynced), diagnostics: make(map[*pencodeLanguageSession][]lsp.Diagnostic)}
				state.documents[file] = doc
			}
			doc.seen = true
			if doc.content != file.Content {
				doc.version++
				doc.content = file.Content
				for session := range doc.diagnostics {
					model.setProblems(session, doc.uri, nil)
				}
				clear(doc.diagnostics)
			}
			doc.saved = file.savedContent
			for _, server := range state.servers {
				if !server.Match(file.Name) {
					continue
				}
				session := model.startLanguageSession(root, server)
				if session.client == nil || session.failed {
					continue
				}
				synced, exists := doc.servers[session]
				if exists && synced.version == doc.version && synced.saveRevision == file.saveRevision {
					continue
				}
				job := pencodeLanguageJob{document: doc, uri: doc.uri, content: doc.content, version: doc.version, save: exists && synced.saveRevision != file.saveRevision, saved: doc.saved}
				if synced.update.replace(job) {
					doc.servers[session] = pencodeLanguageSynced{version: doc.version, saveRevision: file.saveRevision, update: synced.update}
					continue
				}
				if len(session.jobs) == cap(session.jobs) {
					continue
				}
				update := &pencodeLanguageUpdate{job: job}
				select {
				case session.jobs <- pencodeLanguageJob{update: update}:
					doc.servers[session] = pencodeLanguageSynced{version: doc.version, saveRevision: file.saveRevision, update: update}
				default:
				}
			}
		}
	}
	if len(model.projects) > 0 {
		for _, project := range model.projects {
			visit(project.Path, project, project.Files)
		}
	} else {
		visit(model.projectPath, nil, model.files)
	}
	for file, doc := range state.documents {
		if doc.seen {
			continue
		}
		closed := true
		for session := range doc.servers {
			if session.failed {
				delete(doc.servers, session)
				continue
			}
			select {
			case session.jobs <- pencodeLanguageJob{uri: doc.uri, close: true}:
				delete(doc.servers, session)
			default:
				closed = false
			}
		}
		if closed {
			delete(state.documents, file)
		}
	}
	// Removing a root closes every process, including servers that failed to start.
	for key, session := range state.sessions {
		present := key.root == model.projectPath
		for _, project := range model.projects {
			if project.Path == key.root {
				present = true
				break
			}
		}
		if !present {
			session.cancel()
			delete(state.sessions, key)
			model.clearSessionProblems(session)
		}
	}
}
func (model *workspaceModel) languageDocument() *pencodeLanguageDocument {
	if model.languages == nil {
		return nil
	}
	model.syncLanguageDocuments()
	return model.languages.documents[model.activeFile()]
}
func (model *workspaceModel) pollLanguageServers(world *module.Content) bool {
	state := model.languages
	if state == nil {
		return false
	}
	model.syncLanguageDocuments()
	changed := false
	for range 128 {
		select {
		case event := <-state.events:
			if state.sessions[event.session.key] != event.session {
				continue
			}
			if event.method != "workspace/semanticTokens/refresh" && event.method != "window/logMessage" && event.method != "window/showMessage" && !(event.method == "result" && event.job.semantic) {
				changed = true
			}
			switch event.method {
			case "ready":
				event.session.client = event.client
				model.logOutput("Language Server: "+event.session.server.Label, "语言服务已启动")
				model.syncLanguageDocuments()
			case "failed":
				event.session.failed = true
				model.clearSessionProblems(event.session)
				model.logOutput("Language Server: "+event.session.server.Label, fmt.Sprintf("[Error] %v", event.err))
				for _, doc := range state.documents {
					delete(doc.diagnostics, event.session)
					if doc.semantic != nil && doc.semantic.session == event.session {
						doc.semantic = nil
					}
				}
				model.setActiveStatus(fmt.Sprintf("LSP %s：%v", event.session.server.Label, event.err))
			case "textDocument/publishDiagnostics":
				var p lsp.PublishDiagnosticsParams
				if json.Unmarshal(event.params, &p) != nil {
					continue
				}
				stale := false
				for _, doc := range state.documents {
					if doc.uri != p.URI {
						continue
					}
					stale = p.Version != nil && *p.Version != doc.version
					if !stale {
						doc.diagnostics[event.session] = p.Diagnostics
					}
					break
				}
				if !stale {
					model.setProblems(event.session, p.URI, p.Diagnostics)
				}
			case "workspace/applyEdit":
				var p struct {
					Edit lsp.WorkspaceEdit `json:"edit"`
				}
				reply := event.reply
				complete := func(err error) {
					result := map[string]any{"applied": err == nil}
					if err != nil {
						result["failureReason"] = err.Error()
					}
					select {
					case reply <- pencodeLanguageReply{result: result}:
					default:
					}
				}
				err := json.Unmarshal(event.params, &p)
				if err == nil {
					err = model.applyLanguageWorkspaceEdit(event.session, p.Edit, complete)
				}
				if err != nil {
					complete(err)
				}
			case "window/showDocument":
				var p struct {
					URI       lsp.URI    `json:"uri"`
					External  bool       `json:"external"`
					Selection *lsp.Range `json:"selection"`
				}
				state.panel.session = event.session
				success := json.Unmarshal(event.params, &p) == nil && !p.External && model.navigateLanguageLocation(world, lsp.Location{URI: p.URI}, p.Selection)
				event.reply <- pencodeLanguageReply{result: map[string]bool{"success": success}}
			case "window/showMessageRequest":
				// Surface the request and its actions; the server receives the selected
				// action only after the user clicks it (or null when dismissed).
				model.showLanguageMessage(event)
			case "window/logMessage", "window/showMessage":
				var p struct {
					Message string `json:"message"`
					Type    int    `json:"type"`
				}
				if json.Unmarshal(event.params, &p) == nil {
					model.logOutput("Language Server: "+event.session.server.Label, fmt.Sprintf("[%s] %s", languageMessageLevel(p.Type), p.Message))
					model.refreshRegion(world, pencodeRefreshOutput)
				}
			case "workspace/semanticTokens/refresh":
				for _, doc := range state.documents {
					if doc.semantic != nil && doc.semantic.session == event.session {
						doc.semantic.requested = 0
						doc.semantic.resultID = ""
					}
				}
			case "result":
				if event.job.semantic {
					model.acceptLanguageSemantic(event)
					model.refreshRegion(world, pencodeRefreshEditor)
					continue
				}
				if event.err != nil {
					if event.job.hover {
						if event.job.token == model.languageUI.hover.token {
							model.cancelLanguageHover()
						}
						continue
					}
					if event.job.token == state.panel.token {
						state.panel.pending = false
					}
					if event.job.automatic {
						continue
					}
					if !errors.Is(event.err, context.Canceled) {
						model.setActiveStatus("LSP：" + event.err.Error())
						if event.job.token == state.panel.token {
							state.panel.body = event.err.Error()
						}
					}
					continue
				}
				model.acceptLanguageResult(world, event)
			}
		default:
			model.scheduleLanguageSemantic()
			return changed
		}
	}
	return changed
}

func (model *workspaceModel) prepareLanguageSave(file *openFile) {
	state := model.languages
	if state != nil {
		model.syncLanguageDocuments()
		if doc := state.documents[file]; doc != nil {
			for session := range doc.servers {
				if session.client == nil || session.failed {
					continue
				}
				// Only servers that negotiated willSave receive it.
				select {
				case session.jobs <- pencodeLanguageJob{method: "textDocument/willSave", uri: doc.uri, notify: true, willSave: true}:
				default:
				}
			}
		}
	}

}
