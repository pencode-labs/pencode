package workbench

func (model *workspaceModel) backendConnected() bool {
	if model.backend == nil || model.session != nil && model.session.connection != nil && model.session.connection.restoring {
		return false
	}
	select {
	case <-model.backend.Done():
		return false
	default:
		return true
	}
}

func (f *openFile) closeDocument() {
	if f.document != nil {
		f.document.Release()
		f.document = nil
	}
}
