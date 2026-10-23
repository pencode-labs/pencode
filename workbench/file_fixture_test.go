package workbench

import (
	"context"
	"fmt"
	"github.com/pencode-labs/pencode/client"
	"github.com/pencode-labs/pencode/protocol"
	"io"
	"os"
	"strings"
	"time"
)

// Binary viewers receive a path, never binary bytes disguised as text.
func readPencodeDocument(filename string) (string, error) {
	if isTextFile(filename) {
		return readPencodeContent(filename)
	}
	info, err := os.Stat(filename)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("不是普通文件：%s", filename)
	}
	return "", nil
}

// Build the owned string directly instead of retaining a second, file-sized
// byte slice until the next GC. The read buffer stays on the stack.
func readPencodeContent(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	var content strings.Builder
	if size := info.Size(); size > 0 && size <= int64(int(^uint(0)>>1)) {
		content.Grow(int(size))
	}
	var buffer [32 << 10]byte
	for {
		n, err := file.Read(buffer[:])
		content.Write(buffer[:n])
		if err == io.EOF {
			return content.String(), nil
		}
		if err != nil {
			return "", err
		}
	}
}

func openProjectFile(p *pencodeProject, relative string) {
	if p.Files[relative] != nil {
		selectProjectFile(p, relative, false)
		return
	}
	document, content, err := p.openDocument(relative)
	if err != nil {
		p.Status = err.Error()
		return
	}
	acceptProjectFile(p, relative, false, document, content)
}
func savePencodeFile(backend *client.Client, projectPath string, file *openFile) error {
	if !isTextFile(file.Name) {
		return fmt.Errorf("当前编辑工具不支持保存二进制文件")
	}
	if file.document == nil {
		document, saved, err := openPencodeDocument(backend, projectPath, file.Path)
		if err != nil {
			return err
		}
		if saved != file.savedContent {
			document.Close()
			return &protocol.Error{Code: protocol.Conflict, Message: "文件已被其他程序修改，请先比较或重新加载"}
		}
		file.document = document
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := file.document.Save(ctx, file.Content)
	if err != nil {
		return fmt.Errorf("保存 %s 失败：%w", file.Name, err)
	}
	file.Dirty = false
	file.saveRevision++
	file.savedContent = file.Content
	return nil
}

func openPencodeDocument(backend *client.Client, root, relative string) (*client.Document, string, error) {
	if backend == nil {
		return nil, "", fmt.Errorf("工作区服务未连接")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	w, err := backend.OpenWorkspace(ctx, root)
	if err != nil {
		return nil, "", err
	}
	return backend.OpenDocument(ctx, w.ID, relative, isTextFile(relative))
}

func (p *pencodeProject) openDocument(relative string) (*client.Document, string, error) {
	if p.backend == nil {
		return nil, "", fmt.Errorf("工作区服务未连接")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return p.backend.OpenDocument(ctx, p.workspace.ID, relative, isTextFile(relative))
}
