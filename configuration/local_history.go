package configuration

import "math"

// LocalHistorySettings uses the same setting names and units as VS Code.
type LocalHistorySettings struct {
	Exclude        map[string]bool `json:"workbench.localHistory.exclude,omitempty"`
	MaxFileSize    int64           `json:"workbench.localHistory.maxFileSize"`
	MaxFileEntries int             `json:"workbench.localHistory.maxFileEntries"`
	MergeWindow    int64           `json:"workbench.localHistory.mergeWindow"`
	Enabled        bool            `json:"workbench.localHistory.enabled"`
}

func DefaultLocalHistory() LocalHistorySettings {
	return LocalHistorySettings{Enabled: true, MaxFileSize: 256, MaxFileEntries: 50, MergeWindow: 10}
}

func (s *LocalHistorySettings) Validate() {
	defaults := DefaultLocalHistory()
	if s.MaxFileSize < 1 || s.MaxFileSize > math.MaxInt64/1024 {
		s.MaxFileSize = defaults.MaxFileSize
	}
	if s.MaxFileEntries < 0 {
		s.MaxFileEntries = defaults.MaxFileEntries
	}
	if s.MergeWindow < 1 || s.MergeWindow > math.MaxInt64/1_000_000_000 {
		s.MergeWindow = defaults.MergeWindow
	}
}
