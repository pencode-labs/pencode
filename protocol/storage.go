package protocol

type StorageKind uint8

const (
	StorageLargeHistory StorageKind = iota
	StorageHistory
	StorageProfiles
	StorageSearchCache
	StorageOtherProfiles
	StorageExtensions
	StorageTools
	StorageOther
	StorageKinds
)

type StorageMask uint32

func (k StorageKind) Mask() StorageMask { return 1 << k }

const CleanableStorage StorageMask = 1<<StorageLargeHistory | 1<<StorageHistory | 1<<StorageProfiles | 1<<StorageSearchCache

type StorageSize struct {
	Bytes uint64
	Files uint64
}

// Sizes are logical file lengths after any requested cleanup.
type StorageReport struct {
	Host    string
	Problem string
	Sizes   [StorageKinds]StorageSize
	Removed StorageSize
}
