package diskusage

type DiskCategoryItem struct {
	Name        string  `json:"name" cbor:"0,keyasint"`
	Category    string  `json:"category" cbor:"1,keyasint"`
	Path        string  `json:"path" cbor:"2,keyasint"`
	Size        uint64  `json:"size" cbor:"3,keyasint"`
	SizeHuman   string  `json:"sizeHuman" cbor:"4,keyasint"`
	PercentDisk float64 `json:"percentDisk" cbor:"5,keyasint"`
	Status      string  `json:"status" cbor:"6,keyasint"`
	CleanupCmd  string  `json:"cleanupCmd" cbor:"7,keyasint"`
	Description string  `json:"description" cbor:"8,keyasint"`
}

type DiskUsageReport struct {
	TotalBytes   uint64             `json:"totalBytes" cbor:"0,keyasint"`
	UsedBytes    uint64             `json:"usedBytes" cbor:"1,keyasint"`
	FreeBytes    uint64             `json:"freeBytes" cbor:"2,keyasint"`
	UsedPercent  float64            `json:"usedPercent" cbor:"3,keyasint"`
	RootMount    string             `json:"rootMount" cbor:"4,keyasint"`
	Categories   []DiskCategoryItem `json:"categories" cbor:"5,keyasint"`
	Timestamp    int64              `json:"timestamp" cbor:"6,keyasint"`
	ScannedPaths int                `json:"scannedPaths" cbor:"7,keyasint"`
}
